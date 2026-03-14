package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := "stress_test_dir"
	os.RemoveAll(root)
	os.MkdirAll(root, 0755)

	fmt.Printf("Creating 10,000 files in %s...\n", root)
	for i := 1; i <= 10000; i++ {
		filename := filepath.Join(root, fmt.Sprintf("file_%05d.txt", i))
		content := []byte(fmt.Sprintf("This is content of file number %d", i))
		if err := os.WriteFile(filename, content, 0644); err != nil {
			fmt.Printf("Error writing file %d: %v\n", i, err)
			return
		}
		if i%1000 == 0 {
			fmt.Printf("Created %d files...\n", i)
		}
	}
	fmt.Println("Done.")
}
