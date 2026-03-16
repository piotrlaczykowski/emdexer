package vfs

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SFTPFileSystem struct {
	client     *ssh.Client
	sftpClient *sftp.Client
}

func NewSFTPFileSystem(host, port, user, password string) (*SFTPFileSystem, error) {
	// Security: Use knownhosts for verification (Trust-On-First-Use)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}
	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")

	// Ensure the file exists (even if empty) to avoid callback failure if missing
	if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0700); err != nil {
			return nil, fmt.Errorf("failed to create .ssh dir: %w", err)
		}
		if err := os.WriteFile(knownHostsPath, []byte(""), 0600); err != nil {
			return nil, fmt.Errorf("failed to create known_hosts: %w", err)
		}
	}

	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load known_hosts: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
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

func (s *SFTPFileSystem) CheckPermissions() error {
	_, err := s.sftpClient.ReadDir(".")
	if err != nil {
		return fmt.Errorf("permission check failed: %w (ensure user has access to path)", err)
	}
	return nil
}

func (s *SFTPFileSystem) Close() error {
	s.sftpClient.Close()
	return s.client.Close()
}
