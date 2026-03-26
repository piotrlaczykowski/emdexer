package indexer

import (
	"fmt"
	"strings"
)

const contextPrompt = `You are an AI assistant helping with document retrieval.
Given the following document excerpt, write 1-2 sentences describing what it is
about and its key topics. Be concise. Respond with only the summary, no preamble.

Document:
%s`

const maxDocContextChars = 4000
const minDocLengthForContext = 200

// BuildDocContext generates a short context summary for a document using the
// provided LLM function. Returns empty string on error (non-fatal).
func BuildDocContext(docText string, llmFn func(prompt string) (string, error)) string {
	if len(docText) < minDocLengthForContext || llmFn == nil {
		return ""
	}
	excerpt := docText
	if len(excerpt) > maxDocContextChars {
		excerpt = excerpt[:maxDocContextChars]
	}
	prompt := fmt.Sprintf(contextPrompt, excerpt)
	ctx, err := llmFn(prompt)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(ctx)
}

// EnrichChunkWithContext prepends the document context to a chunk for embedding.
// The original chunk text is returned unchanged for storage in the payload.
func EnrichChunkWithContext(chunk, docContext string) string {
	if docContext == "" {
		return chunk
	}
	return "[Context: " + docContext + "]\n" + chunk
}
