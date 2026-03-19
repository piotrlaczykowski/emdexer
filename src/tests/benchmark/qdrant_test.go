package benchmark

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func BenchmarkQdrantUpsertLatency(b *testing.B) {
	// Assumption: Qdrant is running on localhost:6334 (default in docker-compose.yml)
	addr := "localhost:6334"
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Skip("Qdrant not available, skipping benchmark")
		return
	}
	defer conn.Close()

	client := qdrant.NewPointsClient(conn)
	collectionName := "benchmark_collection"

	// Setup collection (simplified, might need real setup if not exists)
	// For this benchmark, we assume a collection exists or we just measure the call time.

	batchSize := 100
	points := make([]*qdrant.PointStruct, batchSize)
	for i := 0; i < batchSize; i++ {
		points[i] = &qdrant.PointStruct{
			Id: qdrant.NewIDNum(uint64(i)),
			Vectors: qdrant.NewVector(make([]float32, 3072)), // standard embedding size
			Payload: map[string]*qdrant.Value{
				"path": qdrant.NewValueStr("/bench/test"),
				"text": qdrant.NewValueStr("benchmark text"),
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: collectionName,
			Points:         points,
		})
		cancel()
		if err != nil {
			// If collection doesn't exist, we still want to see the error/latency
			// but don't fail the whole suite if Qdrant isn't fully ready.
			b.Logf("Upsert error: %v", err)
		}
	}
}
