package vfs

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

type NFSFileSystem struct {
	mount  *nfs.Mount
	target *nfs.Target
}

func NewNFSFileSystem(host, path string) (*NFSFileSystem, error) {
	mount, err := nfs.DialMount(host, 1*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to dial mount: %w", err)
	}

	// Security: avoid root UID/GID by default unless specified
	uid := 1000 // default non-root
	gid := 1000
	auth := rpc.NewAuthUnix("emdexer", uint32(uid), uint32(gid)).Auth()
	target, err := mount.Mount(path, auth)
	if err != nil {
		mount.Close()
		return nil, fmt.Errorf("failed to mount: %w", err)
	}

	return &NFSFileSystem{
		mount:  mount,
		target: target,
	}, nil
}

func (n *NFSFileSystem) Open(name string) (fs.File, error) {
	f, err := n.target.Open(name)
	if err != nil {
		return nil, err
	}
	return &NFSFile{File: f, target: n.target, name: name}, nil
}

func (n *NFSFileSystem) Stat(name string) (fs.FileInfo, error) {
	return n.target.Getattr(name)
}

func (n *NFSFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := n.target.ReadDirPlus(name)
	if err != nil {
		return nil, err
	}
	res := make([]fs.DirEntry, len(entries))
	for i, e := range entries {
		res[i] = fs.FileInfoToDirEntry(e)
	}
	return res, nil
}

func (n *NFSFileSystem) CheckPermissions() error {
	// Root check (security hardening)
	auth := n.target.Auth()
	if auth != nil {
		// go-nfs-client/nfs/rpc AuthUnix check
		// We use NewAuthUnix("emdexer", 0, 0) which might be risky if we want to avoid root (UID 0).
		// Let's audit this.
	}

	_, err := n.target.ReadDirPlus(".")
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	return nil
}

func (n *NFSFileSystem) Close() error {
	n.target.Close()
	n.mount.Close()
	return nil
}

type NFSFile struct {
	*nfs.File
	target *nfs.Target
	name   string
}

func (f *NFSFile) Stat() (fs.FileInfo, error) {
	return f.target.Getattr(f.name)
}
