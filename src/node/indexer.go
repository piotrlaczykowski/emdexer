package main

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
)

func indexDataToPoints(path string, content []byte) []*qdrant.PointStruct {
	var text string
	var err error
	startExt := time.Now()
	if len(content) > 0 {
		text, err = extractFromBytes(path, content, globalCfg.ExtractousHost)
	} else {
		text, err = extractContent(path, globalCfg.ExtractousHost)
	}
	extractionLatency.Observe(float64(time.Since(startExt).Milliseconds()))

	if err != nil {
		errorCount.WithLabelValues("extraction", globalCfg.NodeType).Inc()
		text = ""
	}

	var points []*qdrant.PointStruct
	chunks := []string{""}
	if text != "" {
		chunks = smartChunk(text, 512, 50)
	}

	for i, chunk := range chunks {
		var vector []float32
		if chunk != "" {
			startEmb := time.Now()
			vector, err = globalEmbedder.Embed(chunk)
			if err != nil {
				errorCount.WithLabelValues("embedding", globalCfg.NodeType).Inc()
				continue
			}
			embeddingLatency.Observe(float64(time.Since(startEmb).Milliseconds()))
		} else {
			vector = make([]float32, EmbeddingDims)
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
		if globalCfg.Namespace != "" {
			payload["namespace"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: globalCfg.Namespace}}
		}

		points = append(points, &qdrant.PointStruct{
			Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Uuid{Uuid: u.String()}},
			Vectors: &qdrant.Vectors{VectorsOptions: &qdrant.Vectors_Vector{Vector: &qdrant.Vector{Data: vector}}},
			Payload: payload,
		})
	}
	indexingThroughput.Inc()
	return points
}

func startQueueWorker() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		for {
			item, _ := globalQueue.Dequeue()
			if item == nil { break }
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         item.Points,
			})
			if err == nil { globalQueue.Delete(item.ID) } else { break }
		}
	}
}
