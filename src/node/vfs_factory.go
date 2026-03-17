package main

import (
	"github.com/piotrlaczykowski/emdexer/vfs"
)

var globalFS vfs.FileSystem

func initVFS(cfg Config) {
	var err error
	switch cfg.NodeType {
	case "smb":
		globalFS, err = vfs.NewSMBFileSystem(cfg.SMBHost, cfg.SMBUser, cfg.SMBPass, cfg.SMBShare)
	case "sftp":
		globalFS, err = vfs.NewSFTPFileSystem(cfg.SFTPHost, cfg.SFTPPort, cfg.SFTPUser, cfg.SFTPPass)
	case "nfs":
		globalFS, err = vfs.NewNFSFileSystem(cfg.NFSHost, cfg.NFSPath)
	case "s3":
		globalFS, err = vfs.NewS3FileSystem(globalCtx, cfg.S3Bucket, vfs.S3Options{
			Endpoint:     cfg.S3Endpoint,
			AccessKey:    cfg.S3AccessKey,
			SecretKey:    cfg.S3SecretKey,
			Region:       cfg.S3Region,
			UsePathStyle: cfg.S3UsePathStyle,
			Prefix:       cfg.S3Prefix,
		})
	default:
		globalFS = &vfs.OSFileSystem{}
	}
	if err != nil {
		panic(err)
	}
}
