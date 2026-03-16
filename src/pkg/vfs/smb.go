package vfs

import (
	"fmt"
	"io/fs"
	"net"

	"github.com/hirochachacha/go-smb2"
)

type SMBFileSystem struct {
	session *smb2.Session
	share   *smb2.Share
}

func NewSMBFileSystem(host, user, password, shareName string) (*SMBFileSystem, error) {
	conn, err := net.Dial("tcp", host+":445")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SMB host: %w", err)
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     user,
			Password: password,
		},
	}

	s, err := d.Dial(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SMB: %w", err)
	}

	share, err := s.Mount(shareName)
	if err != nil {
		s.Logoff()
		return nil, fmt.Errorf("failed to mount SMB share: %w", err)
	}

	return &SMBFileSystem{
		session: s,
		share:   share,
	}, nil
}

func (s *SMBFileSystem) Open(name string) (fs.File, error) {
	return s.share.Open(name)
}

func (s *SMBFileSystem) Stat(name string) (fs.FileInfo, error) {
	return s.share.Lstat(name)
}

func (s *SMBFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	infos, err := s.share.ReadDir(name)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, len(infos))
	for i, info := range infos {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	return entries, nil
}

func (s *SMBFileSystem) CheckPermissions() error {
	_, err := s.share.ReadDir(".")
	if err != nil {
		return fmt.Errorf("permission check failed on SMB share: %w", err)
	}
	return nil
}

func (s *SMBFileSystem) Close() error {
	s.share.Umount()
	return s.session.Logoff()
}
