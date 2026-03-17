package main

import (
	"os"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

var globalFS vfs.FileSystem

func initVFS() {
	var err error
	switch globalCfg.NodeType {
	case "smb":
		globalFS, err = vfs.NewSMBFileSystem(globalCfg.SMBHost, globalCfg.SMBUser, globalCfg.SMBPass, globalCfg.SMBShare)
	case "sftp":
		globalFS, err = vfs.NewSFTPFileSystem(globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser, globalCfg.SFTPPass)
	case "nfs":
		globalFS, err = vfs.NewNFSFileSystem(globalCfg.NFSHost, globalCfg.NFSPath)
	case "s3":
		globalFS, err = vfs.NewS3FileSystem(globalCtx, globalCfg.S3Bucket, vfs.S3Options{
			Endpoint:     globalCfg.S3Endpoint,
			AccessKey:    globalCfg.S3AccessKey,
			SecretKey:    globalCfg.S3SecretKey,
			Region:       globalCfg.S3Region,
			UsePathStyle: globalCfg.S3UseSSL != "true",
			Prefix:       os.Getenv("NODE_ROOT"),
		})
	default:
		globalFS = &vfs.OSFileSystem{}
	}
	if err != nil {
		panic(err)
	}
}
