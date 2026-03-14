package benchmark

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/laczyk/emdexer/node/pkg/node"
	"github.com/laczyk/emdexer/node/pkg/vfs"
)

func BenchmarkIndexingThroughput(b *testing.B) {
	// Create a temporary directory with many small files
	tmpDir, err := ioutil.TempDir("", "emdexer-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	fileCount := 100
	for i := 0; i < fileCount; i++ {
		err := ioutil.WriteFile(
			filepath.Join(tmpDir, fmt.Sprintf("file_%d.txt", i)),
			[]byte("this is some dummy content for benchmarking indexing throughput"),
			0644,
		)
		if err != nil {
			b.Fatal(err)
		}
	}

	fs := &vfs.OSFileSystem{}
	indexer := node.NewIndexer(fs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		err := indexer.Walk(tmpDir, func(path string, isDir bool, content []byte) error {
			count++
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
