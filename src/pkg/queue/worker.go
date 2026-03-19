package queue

import (
	"context"
	"log"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// StartWorker periodically drains the persistent queue and retries Qdrant upserts.
func StartWorker(q *PersistentQueue, pc qdrant.PointsClient, collection string, ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		for {
			item, _ := q.Dequeue()
			if item == nil {
				break
			}
			_, err := pc.Upsert(ctx, &qdrant.UpsertPoints{
				CollectionName: collection,
				Points:         item.Points,
			})
			if err == nil {
				_ = q.Delete(item.ID)
			} else {
				log.Printf("[queue] retry failed: %v", err)
				break
			}
		}
	}
}
