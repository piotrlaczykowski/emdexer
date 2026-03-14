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

	auth := rpc.NewAuthUnix("emdexer", 0, 0).Auth()
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
