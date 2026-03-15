package vfs

import (
	"fmt"
	"io/fs"
	"net"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SFTPFileSystem struct {
	client     *ssh.Client
	sftpClient *sftp.Client
}

func NewSFTPFileSystem(host, port, user, password string) (*SFTPFileSystem, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106
	}

	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH: %w", err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	return &SFTPFileSystem{
		client:     client,
		sftpClient: sftpClient,
	}, nil
}

func (s *SFTPFileSystem) Open(name string) (fs.File, error) {
	return s.sftpClient.Open(name)
}

func (s *SFTPFileSystem) Stat(name string) (fs.FileInfo, error) {
	return s.sftpClient.Lstat(name)
}

func (s *SFTPFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	infos, err := s.sftpClient.ReadDir(name)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, len(infos))
	for i, info := range infos {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	return entries, nil
}

func (s *SFTPFileSystem) Close() error {
	s.sftpClient.Close()
	return s.client.Close()
}
