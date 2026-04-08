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

// EmbedBatchFn is a function that embeds multiple texts in one call.
type EmbedBatchFn func(texts []string) ([][]float32, error)

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
	MaxChunkWords int          // max words per chunk (default 512)
	Embedder      EmbedFn      // used when BatchEmbedder is nil
	BatchEmbedder EmbedBatchFn // preferred; uses Embedder serial fallback if nil
	Threshold     float32      // cosine similarity threshold (default 0.7)
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

	if s.Embedder == nil && s.BatchEmbedder == nil {
		return fallback(text)
	}

	sentences := splitSentences(text)
	if len(sentences) <= 3 {
		return fallback(text)
	}

	// Embed all sentences — use batch path when available, serial otherwise.
	var vecs [][]float32
	if s.BatchEmbedder != nil {
		var err error
		vecs, err = s.BatchEmbedder(sentences)
		if err != nil {
			log.Printf("[indexer] semantic-chunker: batch embedding failed, falling back: %v", err)
			return fallback(text)
		}
	} else {
		for _, sent := range sentences {
			v, err := s.Embedder(sent)
			if err != nil {
				log.Printf("[indexer] semantic-chunker: embedding failed, falling back: %v", err)
				return fallback(text)
			}
			vecs = append(vecs, v)
		}
	}

	if len(vecs) != len(sentences) {
		log.Printf("[indexer] semantic-chunker: vector count mismatch (%d vecs, %d sentences), falling back", len(vecs), len(sentences))
		return fallback(text)
	}

	// Greedily group sentences using an O(n) running sum as centroid.
	dims := len(vecs[0])
	runSum := make([]float32, dims)
	copy(runSum, vecs[0])
	groupLen := 1
	groupStart := 0

	var chunks []string

	for i := 1; i < len(sentences); i++ {
		// centroid = runSum / groupLen
		centroid := make([]float32, dims)
		for d := range centroid {
			centroid[d] = runSum[d] / float32(groupLen)
		}
		sim := CosineSimilarity(centroid, vecs[i])

		groupWords := wordCount(sentences[groupStart:i])
		nextWords := len(strings.Fields(sentences[i]))

		if sim < threshold || groupWords+nextWords > maxWords {
			chunks = append(chunks, strings.Join(sentences[groupStart:i], " "))
			groupStart = i
			copy(runSum, vecs[i])
			groupLen = 1
		} else {
			for d := range runSum {
				runSum[d] += vecs[i][d]
			}
			groupLen++
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
