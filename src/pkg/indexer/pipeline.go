package indexer

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/plugin"
	"github.com/qdrant/go-client/qdrant"
)

const ProjectNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// Extractor is a function that extracts text from file content.
// The second return value carries optional extra metadata (e.g. whisper segments,
// extraction_method) to be merged into the Qdrant point payload on chunk 0.
type Extractor func(path string, content []byte, host string) (string, map[string]string, error)

// PipelineConfig holds injected dependencies for the indexing pipeline.
type PipelineConfig struct {
	Namespace      string
	ExtractousHost string
	NodeType       string
	Embedder       embed.EmbedProvider
	Extract        Extractor
	// Plugins is the list of loaded extractor plugins (may be nil).
	// A plugin whose Extensions() list includes a file's extension takes
	// priority over the default Extractous/Whisper extraction path.
	Plugins []plugin.ExtractorPlugin
}

// IndexDataToPoints converts file content into Qdrant points via extraction, chunking, and embedding.
func IndexDataToPoints(path string, content []byte, cfg PipelineConfig) []*qdrant.PointStruct {
	var text string
	var err error
	var pluginRels []plugin.Relation
	var extraMeta map[string]string

	// Check whether a loaded plugin handles this file extension.
	// Plugins take priority over the default Extractous/Whisper path.
	ext := strings.ToLower(filepath.Ext(path))
	var matched plugin.ExtractorPlugin
	for _, p := range cfg.Plugins {
		for _, pe := range p.Extensions() {
			if pe == ext {
				matched = p
				break
			}
		}
		if matched != nil {
			break
		}
	}

	if matched != nil && len(content) > 0 {
		text, pluginRels, err = matched.Extract(context.Background(), filepath.Base(path), content)
		if err != nil {
			log.Printf("[plugin] %s failed for %s: %v", matched.Name(), path, err)
			text = ""
		}
	} else if len(content) > 0 {
		text, extraMeta, err = cfg.Extract(path, content, cfg.ExtractousHost)
		if err != nil {
			log.Printf("[node] Extraction failed for %s: %v", path, err)
			text = ""
		}
	} else {
		text, extraMeta, err = cfg.Extract(path, nil, cfg.ExtractousHost)
		if err != nil {
			log.Printf("[node] Extraction failed for %s: %v", path, err)
			text = ""
		}
	}

	text = strings.TrimSpace(text)
	if len(text) < 10 {
		log.Printf("[node] WARN: Skipping %s — extraction too short (%d chars, min 10)", path, len(text))
		return nil
	}

	chunks := SmartChunk(text, 512, 50)
	if len(chunks) == 0 {
		log.Printf("[node] WARN: Skipping %s — chunking produced no segments", path)
		return nil
	}

	// Compute structural relations for chunk 0.
	// If the plugin returned relations, use those; otherwise derive them from the extracted text.
	var relationsJSON string
	if len(pluginRels) > 0 {
		rels := make([]Relation, len(pluginRels))
		for i, r := range pluginRels {
			rels[i] = Relation{Type: r.Type, Target: r.Target, Name: r.Name}
		}
		relationsJSON = RelationsToJSON(rels)
	} else {
		relationsJSON = RelationsToJSON(ExtractRelations(path, text))
	}

	var points []*qdrant.PointStruct
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		vector, embErr := cfg.Embedder.Embed(context.Background(), chunk)
		if embErr != nil {
			LogEmbeddingError(path, i, embErr)
			continue
		}

		if IsZeroVector(vector) {
			log.Printf("[node] WARN: Skipping %s (chunk %d) — embedding returned zero-vector", path, i)
			continue
		}

		ns := uuid.MustParse(ProjectNamespace)
		idInput := fmt.Sprintf("%s:%d", path, i)
		u := uuid.NewSHA1(ns, []byte(idInput))

		payload := map[string]*qdrant.Value{
			"path":       {Kind: &qdrant.Value_StringValue{StringValue: path}},
			"chunk":      {Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(i)}},
			"text":       {Kind: &qdrant.Value_StringValue{StringValue: chunk}},
			"indexed_at": {Kind: &qdrant.Value_IntegerValue{IntegerValue: time.Now().UnixNano()}},
		}
		if cfg.Namespace != "" {
			payload["namespace"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: cfg.Namespace}}
		}
		// Attach relations to chunk 0 only — the graph builder only needs one point per file.
		if i == 0 && relationsJSON != "" {
			payload["relations"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: relationsJSON}}
		}
		// Merge extractor-provided metadata (e.g. whisper segments, extraction_method) into chunk 0.
		if i == 0 {
			for k, v := range extraMeta {
				payload[k] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: v}}
			}
		}

		points = append(points, &qdrant.PointStruct{
			Id:      &qdrant.PointId{PointIdOptions: &qdrant.PointId_Uuid{Uuid: u.String()}},
			Vectors: &qdrant.Vectors{VectorsOptions: &qdrant.Vectors_Vector{Vector: &qdrant.Vector{Data: vector}}},
			Payload: payload,
		})
	}
	return points
}

// IsZeroVector returns true if all elements in the vector are zero.
func IsZeroVector(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}
