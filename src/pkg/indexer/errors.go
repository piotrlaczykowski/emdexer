package indexer

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// LogEmbeddingError logs embedding failures with actionable instructions to stderr.
func LogEmbeddingError(path string, chunk int, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "Unauthorized"):
		fmt.Fprintf(os.Stderr, "[node] ERROR: Embedding auth failed for %s (chunk %d): %v\n"+
			"  → Check your GOOGLE_API_KEY in .env — it may be invalid or expired.\n"+
			"  → Generate a new key at https://aistudio.google.com/app/apikey\n", path, chunk, err)
	case strings.Contains(msg, "429") || strings.Contains(msg, "Rate Limit") || strings.Contains(msg, "RESOURCE_EXHAUSTED"):
		fmt.Fprintf(os.Stderr, "[node] ERROR: Embedding rate-limited for %s (chunk %d): %v\n"+
			"  → You've hit the API rate limit. Reduce EMDEX_BATCH_SIZE or add a delay.\n"+
			"  → Consider switching to EMBED_PROVIDER=ollama for unlimited local embeddings.\n", path, chunk, err)
	case strings.Contains(msg, "403") || strings.Contains(msg, "Forbidden"):
		fmt.Fprintf(os.Stderr, "[node] ERROR: Embedding forbidden for %s (chunk %d): %v\n"+
			"  → Your API key may lack the required permissions. Check the Gemini API console.\n", path, chunk, err)
	default:
		log.Printf("[node] Embedding failed for %s (chunk %d): %v", path, chunk, err)
	}
}
