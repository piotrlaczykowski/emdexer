package metadata_test

import (
	"context"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMetadataSchema(t *testing.T) {
	// This test verifies that points retrieved from Qdrant contain the required schema.
	addr := "localhost:6334"
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Skip("Qdrant not available")
		return
	}
	defer conn.Close()

	client := qdrant.NewPointsClient(conn)
	
	// Query the first point from emdexer_v1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	limit := uint32(1)
	resp, err := client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: "emdexer_v1",
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})

	if err != nil {
		t.Fatalf("Failed to scroll Qdrant: %v", err)
	}

	if len(resp.Result) == 0 {
		t.Skip("No data in Qdrant to verify schema")
		return
	}

	payload := resp.Result[0].Payload
	requiredFields := []string{"path", "chunk", "text", "namespace", "indexed_at"}
	
	for _, field := range requiredFields {
		if _, ok := payload[field]; !ok {
			t.Errorf("Missing required metadata field: %s", field)
		}
	}
}
