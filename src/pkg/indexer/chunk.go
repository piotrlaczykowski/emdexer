package indexer

import (
	"log"
	"math"
	"strings"
)

// SmartChunk splits text into overlapping chunks by word boundaries.
func SmartChunk(text string, size, overlap int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var chunks []string
	step := size - overlap
	if step <= 0 {
		step = 1
	}
	for i := 0; i < len(words); i += step {
		end := i + size
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

// EmbedFn is a function that embeds a text string into a dense vector.
type EmbedFn func(text string) ([]float32, error)

// ChunkStrategy splits a document into chunks for embedding.
type ChunkStrategy interface {
	Chunk(text string) []string
}

// FixedChunker splits text by word count with overlap. Wraps SmartChunk.
type FixedChunker struct {
	Size    int // words per chunk; default 512
	Overlap int // overlapping words; default 50
}

func (f FixedChunker) Chunk(text string) []string {
	size := f.Size
	if size <= 0 {
		size = 512
	}
	overlap := f.Overlap
	if overlap <= 0 {
		overlap = 50
	}
	if overlap >= size {
		overlap = size / 10
	}
	return SmartChunk(text, size, overlap)
}

// SemanticChunker splits text by sentence boundaries, grouping sentences
// by embedding similarity. Falls back to FixedChunker if the embedder is
// nil, the text has ≤ 3 sentences, or any embedding call fails.
type SemanticChunker struct {
	MaxChunkWords int     // max words per chunk (default 512)
	Embedder      EmbedFn
	Threshold     float32 // cosine similarity threshold (default 0.7)
}

func (s SemanticChunker) Chunk(text string) []string {
	maxWords := s.MaxChunkWords
	if maxWords <= 0 {
		maxWords = 512
	}
	threshold := s.Threshold
	if threshold <= 0 {
		threshold = 0.7
	}

	fallback := FixedChunker{Size: maxWords}.Chunk

	if s.Embedder == nil {
		return fallback(text)
	}

	sentences := splitSentences(text)
	if len(sentences) <= 3 {
		return fallback(text)
	}

	// Embed each sentence.
	vecs := make([][]float32, 0, len(sentences))
	for _, sent := range sentences {
		v, err := s.Embedder(sent)
		if err != nil {
			log.Printf("[indexer] semantic-chunker: embedding failed, falling back: %v", err)
			return fallback(text)
		}
		vecs = append(vecs, v)
	}

	// Greedily group sentences into chunks.
	var chunks []string
	groupStart := 0

	for i := 1; i < len(sentences); i++ {
		// Compute centroid of current group.
		centroid := meanVec(vecs[groupStart:i])
		sim := CosineSimilarity(centroid, vecs[i])

		groupWords := wordCount(sentences[groupStart:i])
		nextWords := len(strings.Fields(sentences[i]))

		// Start new chunk if: similarity drops below threshold OR adding next
		// sentence would exceed MaxChunkWords.
		if sim < threshold || groupWords+nextWords > maxWords {
			chunks = append(chunks, strings.Join(sentences[groupStart:i], " "))
			groupStart = i
		}
	}
	// Flush remaining sentences.
	if groupStart < len(sentences) {
		chunks = append(chunks, strings.Join(sentences[groupStart:], " "))
	}

	if len(chunks) == 0 {
		return fallback(text)
	}
	return chunks
}

// splitSentences splits text into sentences on ". ", "? ", "! ", "\n\n".
// Minimum sentence length: 10 chars.
func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, ". ", ".\x00")
	text = strings.ReplaceAll(text, "? ", "?\x00")
	text = strings.ReplaceAll(text, "! ", "!\x00")
	text = strings.ReplaceAll(text, "\n\n", "\n\n\x00")

	raw := strings.Split(text, "\x00")
	var sentences []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if len(s) >= 10 {
			sentences = append(sentences, s)
		}
	}
	return sentences
}

// CosineSimilarity returns the cosine similarity between two vectors.
// Returns 0 if either vector is zero-length or has zero magnitude.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float32
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(magA))) * float32(math.Sqrt(float64(magB))))
}

// meanVec computes the element-wise mean of a slice of vectors.
func meanVec(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	out := make([]float32, len(vecs[0]))
	for _, v := range vecs {
		for i, x := range v {
			out[i] += x
		}
	}
	n := float32(len(vecs))
	for i := range out {
		out[i] /= n
	}
	return out
}

// wordCount returns the total word count for a slice of sentences.
func wordCount(sentences []string) int {
	total := 0
	for _, s := range sentences {
		total += len(strings.Fields(s))
	}
	return total
}
