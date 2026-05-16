package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	bolt "go.etcd.io/bbolt"
)

var (
	tempMu    sync.Mutex
	tempFiles []string
)

func trackTempFile(path string) {
	tempMu.Lock()
	tempFiles = append(tempFiles, path)
	tempMu.Unlock()
}

func cleanupTempFiles() {
	tempMu.Lock()
	for _, f := range tempFiles {
		os.Remove(f)
	}
	tempFiles = nil
	tempMu.Unlock()
}

func copyToTemp(src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp("", "raft-inspector-*.db")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("copying %s: %w", src, err)
	}
	tmp.Close()
	trackTempFile(tmp.Name())
	return tmp.Name(), nil
}

func openStore(path string) (*raftboltdb.BoltStore, string, error) {
	tmpPath, err := copyToTemp(path)
	if err != nil {
		return nil, "", fmt.Errorf("opening raft.db: %w", err)
	}
	store, err := raftboltdb.New(raftboltdb.Options{
		Path:        tmpPath,
		BoltOptions: &bolt.Options{ReadOnly: true},
	})
	if err != nil {
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("opening raft.db: %w", err)
	}
	return store, tmpPath, nil
}

func openVaultDB(dataDir string) (*bolt.DB, string, error) {
	vaultPath := filepath.Join(dataDir, "vault.db")
	fi, err := os.Stat(vaultPath)
	if err != nil || fi.Size() == 0 {
		return nil, "", fmt.Errorf("vault.db is empty or missing (node may not have been initialized yet)")
	}
	tmpPath, err := copyToTemp(vaultPath)
	if err != nil {
		return nil, "", err
	}
	db, err := bolt.Open(tmpPath, 0600, &bolt.Options{ReadOnly: true, PreLoadFreelist: true})
	if err != nil {
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("opening vault.db: %w", err)
	}
	return db, tmpPath, nil
}
