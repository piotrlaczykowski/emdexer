package registry

import (
	"context"
	"log"

	"github.com/qdrant/go-client/qdrant"
)

// EnsureTextIndexes creates full-text payload indexes on the "text" and "namespace"
// fields of the given collection. These indexes are required for BM25 keyword search
// in the hybrid search pipeline (Phase 21).
//
// Errors are logged but not fatal — if indexing fails the gateway falls back to
// pure vector search transparently. Index creation is idempotent: re-running on an
// already-indexed collection is a no-op in Qdrant.
func EnsureTextIndexes(ctx context.Context, pc qdrant.PointsClient, collection string) {
	fields := []struct {
		name      string
		tokenizer qdrant.TokenizerType
	}{
		{"text", qdrant.TokenizerType_Word},
		{"namespace", qdrant.TokenizerType_Whitespace},
	}

	fieldType := qdrant.FieldType_FieldTypeText
	for _, f := range fields {
		_, err := pc.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: collection,
			FieldName:      f.name,
			FieldType:      &fieldType,
			FieldIndexParams: &qdrant.PayloadIndexParams{
				IndexParams: &qdrant.PayloadIndexParams_TextIndexParams{
					TextIndexParams: &qdrant.TextIndexParams{
						Tokenizer: f.tokenizer,
					},
				},
			},
		})
		if err != nil {
			log.Printf("[registry] full-text index on %q/%q: %v (BM25 search may degrade to vector-only)", collection, f.name, err)
		} else {
			log.Printf("[registry] ensured full-text index on %q/%q", collection, f.name)
		}
	}
}
