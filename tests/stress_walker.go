package main

import (
	"emdexer/pkg/node"
	"emdexer/pkg/vfs"
	"fmt"
	"runtime"
	"time"
)

func main() {
	fs := &vfs.OSFileSystem{}
	indexer := node.NewIndexer(fs)

	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	start := time.Now()
	count := 0
	err := indexer.Walk("stress_test_dir", func(path string, isDir bool, content []byte) error {
		count++
		return nil
	})
	elapsed := time.Since(start)

	runtime.ReadMemStats(&m2)

	if err != nil {
		fmt.Printf("Walk failed: %v\n", err)
		return
	}

	fmt.Printf("Walked %d files in %v\n", count, elapsed)
	fmt.Printf("Memory Growth: %d KB\n", (m2.Alloc-m1.Alloc)/1024)
	fmt.Printf("Total Alloc: %d KB\n", m2.TotalAlloc/1024)
	fmt.Printf("Heap Alloc: %d KB\n", m2.HeapAlloc/1024)
}
