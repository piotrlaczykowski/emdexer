package main

import (
	"log"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

func initVFS(root string) {
	var err error
	switch globalCfg.NodeType {
	case "smb":
		globalFS, err = vfs.NewSMBFileSystem(globalCfg.SMBHost, globalCfg.SMBUser, globalCfg.SMBPass, globalCfg.SMBShare)
		if err == nil {
			log.Printf("[node] SMB VFS initialized: host=%s share=%s", globalCfg.SMBHost, globalCfg.SMBShare)
		}
	case "sftp":
		globalFS, err = vfs.NewSFTPFileSystem(globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser, globalCfg.SFTPPass)
		if err == nil {
			log.Printf("[node] SFTP VFS initialized: host=%s port=%s user=%s", globalCfg.SFTPHost, globalCfg.SFTPPort, globalCfg.SFTPUser)
		}
	case "nfs":
		globalFS, err = vfs.NewNFSFileSystem(globalCfg.NFSHost, globalCfg.NFSPath)
		if err == nil {
			log.Printf("[node] NFS VFS initialized: host=%s path=%s", globalCfg.NFSHost, globalCfg.NFSPath)
		}
	case "s3":
		globalFS, err = vfs.NewS3FileSystem(globalCfg.S3Endpoint, globalCfg.S3AccessKey, globalCfg.S3SecretKey, globalCfg.S3Bucket, globalCfg.S3UseSSL)
		if err == nil {
			log.Printf("[node] S3 VFS initialized: endpoint=%s bucket=%s prefix=%q", globalCfg.S3Endpoint, globalCfg.S3Bucket, globalCfg.S3Prefix)
		}
	default:
		globalFS = &vfs.OSFileSystem{Root: root}
		log.Printf("[node] Local VFS initialized: root=%s", root)
	}
	if err != nil { panic(err) }
}
