package main

import (
	"fmt"
	"github.com/google/uuid"
)

func getID(path string, chunkIndex int) string {
	idInput := fmt.Sprintf("%s:%d", path, chunkIndex)
	u := uuid.NewMD5(uuid.NameSpaceOID, []byte(idInput))
	return u.String()
}

func main() {
	// Scenario A: Same content, different names
	path1 := "docs/report_v1.txt"
	path2 := "docs/report_backup.txt"
	
	id1 := getID(path1, 0)
	id2 := getID(path2, 0)
	
	fmt.Printf("Scenario A (Different Names, Same Content):\n")
	fmt.Printf("Path 1: %s -> ID: %s\n", path1, id1)
	fmt.Printf("Path 2: %s -> ID: %s\n", path2, id2)
	if id1 != id2 {
		fmt.Println("Result: Unique points created (Success)")
	} else {
		fmt.Println("Result: ID Collision (Failure)")
	}

	// Scenario B: Same path, re-indexing
	fmt.Printf("\nScenario B (Same Path, Re-indexing):\n")
	id3 := getID(path1, 0)
	fmt.Printf("Path 1 (again): %s -> ID: %s\n", path1, id3)
	if id1 == id3 {
		fmt.Println("Result: Same ID generated (Enables Idempotent Update) (Success)")
	} else {
		fmt.Println("Result: Different ID generated (Failure)")
	}
}
