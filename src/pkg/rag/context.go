package rag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/search"
)

// BuildContext formats search results into a context string for the LLM prompt.
// Each result is tagged with [Source: namespace/path] when available.
func BuildContext(results []search.Result) string {
	var parts []string
	for _, r := range results {
		t, _ := r.Payload["text"].(string)
		if t == "" {
			continue
		}
		ns, _ := r.Payload["source_namespace"].(string)
		path, _ := r.Payload["path"].(string)

		if ns != "" || path != "" {
			parts = append(parts, fmt.Sprintf("[Source: %s/%s]\n%s", ns, path, t))
		} else {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n---\n")
}

// StreamResponse sends an OpenAI-compatible SSE stream to the client.
func StreamResponse(w http.ResponseWriter, model, answer string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	words := strings.Fields(answer)
	for _, word := range words {
		chunk := openai.StreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.DeltaContent{Content: word + " "}}},
		}
		b, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
	}
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}
