package search

import (
	"context"

	"github.com/qdrant/go-client/qdrant"
)

// DeletePointsByPath removes all Qdrant points matching the given file path.
func DeletePointsByPath(ctx context.Context, pc qdrant.PointsClient, collection, path string) error {
	_, err := pc.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						{
							ConditionOneOf: &qdrant.Condition_Field{
								Field: &qdrant.FieldCondition{
									Key: "path",
									Match: &qdrant.Match{
										MatchValue: &qdrant.Match_Keyword{Keyword: path},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	return err
}
