//go:build ignore

package main

import (
	"fmt"
	"io/fs"
	"os"

	"emdexer/pkg/vfs"
)

func main() {
	host := os.Getenv("NFS_HOST")
	path := os.Getenv("NFS_PATH")

	if host == "" || path == "" {
		fmt.Println("Usage: NFS_HOST=... NFS_PATH=... go run test_nfs.go")
		return
	}

	fmt.Printf("Connecting to NFS: %s:%s\n", host, path)
	nfsFS, err := vfs.NewNFSFileSystem(host, path)
	if err != nil {
		fmt.Printf("Failed to initialize NFS VFS: %v\n", err)
		return
	}
	defer nfsFS.Close()

	fmt.Println("Indexing remote export...")
	err = fs.WalkDir(nfsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, _ := d.Info()
		fmt.Printf("  [%s] %s (%d bytes)\n", map[bool]string{true: "DIR", false: "FILE"}[d.IsDir()], path, info.Size())
		return nil
	})

	if err != nil {
		fmt.Printf("WalkDir failed: %v\n", err)
	} else {
		fmt.Println("Indexing complete.")
	}
}
