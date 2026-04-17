package cache

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

// BuildKey returns a deterministic hex-encoded SHA-256 of
//
//	namespace | generation | model | lowercase(trim(query))
//
// The generation embedded in the key is what makes namespace
// invalidation cheap: bumping the counter in storage makes every
// previously-built key unreachable without any scan-and-delete.
//
// Callers must pass the raw user query (not a log-sanitized version):
// coupling the cache identity to log sanitization rules is fragile.
func BuildKey(namespace string, gen int64, model, query string) string {
	normalized := strings.ToLower(strings.TrimSpace(query))
	sum := sha256.Sum256([]byte(
		namespace + "|" + strconv.FormatInt(gen, 10) + "|" + model + "|" + normalized,
	))
	return fmt.Sprintf("%x", sum)
}
