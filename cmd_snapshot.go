package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"google.golang.org/protobuf/encoding/protowire"
)

func cmdSnapshot(file string, showKeys bool, decrypt bool, initFile string, maxValueLen int, limit int) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("opening snapshot: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("reading gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// First pass: read meta.json and SHA256SUMS (small files).
	// For state.bin, process it in streaming fashion.
	var metaJSON []byte
	var sha256sums []byte
	// Track checksums for verification: filename -> sha256 hash of content.
	checksums := map[string]string{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		name := filepath.Base(hdr.Name)
		switch name {
		case "meta.json":
			metaJSON, _ = io.ReadAll(tr)
			h := sha256.Sum256(metaJSON)
			checksums[name] = hex.EncodeToString(h[:])
		case "SHA256SUMS":
			sha256sums, _ = io.ReadAll(tr)
			h := sha256.Sum256(sha256sums)
			checksums[name] = hex.EncodeToString(h[:])
		case "state.bin":
			// Stream state.bin: compute checksum while parsing.
			if err := processStateBin(tr, checksums, showKeys, decrypt, initFile, maxValueLen, limit, metaJSON, sha256sums); err != nil {
				return err
			}
			return nil
		default:
			// Skip unknown entries but compute checksum.
			data, _ := io.ReadAll(tr)
			h := sha256.Sum256(data)
			checksums[name] = hex.EncodeToString(h[:])
		}
	}

	// If we get here, there was no state.bin (unusual). Print what we have.
	printSnapshotMeta(metaJSON)
	printChecksumVerification(sha256sums, checksums)
	return nil
}

func processStateBin(r io.Reader, checksums map[string]string, showKeys bool, decrypt bool, initFile string, maxValueLen int, limit int, metaJSON, sha256sums []byte) error {
	// We need to read state.bin fully for checksum verification and keyring extraction.
	// Use a hash writer to compute checksum while reading.
	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	data, err := io.ReadAll(tee)
	if err != nil {
		return fmt.Errorf("reading state.bin: %w", err)
	}
	checksums["state.bin"] = hex.EncodeToString(hasher.Sum(nil))

	// Print metadata and checksums first.
	printSnapshotMeta(metaJSON)
	printChecksumVerification(sha256sums, checksums)

	// Decrypt keyring if needed.
	var keys map[uint32][]byte
	if decrypt {
		if initFile == "" {
			return fmt.Errorf("--decrypt requires --unseal-key-file")
		}
		rootKey, err := loadRootKey(initFile)
		if err != nil {
			return fmt.Errorf("loading root key: %w", err)
		}
		keys, err = loadKeyringFromStateBin(rootKey, data)
		if err != nil {
			return fmt.Errorf("loading keyring from snapshot: %w", err)
		}
	}

	fmt.Println()
	header.Println("─── State Data ───")
	count, totalSize := parseStateBin(data, showKeys, keys, maxValueLen, limit)
	label.Printf("  %-16s", "Total Keys:")
	value.Printf("%d\n", count)
	label.Printf("  %-16s", "Total Size:")
	value.Printf("%d bytes\n", totalSize)

	fmt.Println()
	dim.Println("  Index            Raft log index at which this snapshot was taken. [meta.json]")
	dim.Println("  Term             Raft term at the time of snapshot. [meta.json]")
	dim.Println("  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]")
	dim.Println("  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]")
	dim.Println("  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]")
	dim.Println("  Total Size       Sum of all value bytes (encrypted); does not include key path sizes. [state.bin]")
	dim.Println("  --keys           Print all key paths; add --decrypt --unseal-key-file to show decrypted values.")
	return nil
}

func printSnapshotMeta(metaJSON []byte) {
	if metaJSON == nil {
		return
	}
	header.Println("─── Snapshot Metadata ───")
	var meta struct {
		Index         uint64 `json:"Index"`
		Term          uint64 `json:"Term"`
		Configuration struct {
			Servers []struct {
				Suffrage int    `json:"Suffrage"`
				ID       string `json:"ID"`
				Address  string `json:"Address"`
			} `json:"Servers"`
		} `json:"Configuration"`
	}
	if err := json.Unmarshal(metaJSON, &meta); err == nil {
		label.Printf("  %-16s", "Index:")
		value.Printf("%d\n", meta.Index)
		label.Printf("  %-16s", "Term:")
		value.Printf("%d\n", meta.Term)
		label.Printf("  %-16s", "Servers:")
		fmt.Println()
		for _, s := range meta.Configuration.Servers {
			value.Printf("    - %s (%s) %s\n", s.ID, s.Address, suffrageName(int32(s.Suffrage)))
		}
	} else {
		fmt.Fprintf(os.Stderr, "  error parsing meta.json: %v\n", err)
	}
}

func printChecksumVerification(sha256sums []byte, checksums map[string]string) {
	if sha256sums == nil {
		return
	}
	fmt.Println()
	header.Println("─── Checksum Verification ───")
	scanner := bufio.NewScanner(strings.NewReader(string(sha256sums)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		expectedHash := parts[0]
		fileName := filepath.Base(parts[1])
		if actual, ok := checksums[fileName]; ok {
			if actual == expectedHash {
				opColor.Printf("  ✓ %s\n", fileName)
			} else {
				color.New(color.FgRed).Printf("  ✗ %s (mismatch)\n", fileName)
			}
		}
	}
}

func parseStateBin(data []byte, showKeys bool, keys map[uint32][]byte, maxValueLen int, limit int) (count int, totalSize int) {
	printed := 0
	for len(data) > 0 {
		msgLen, n := protowire.ConsumeVarint(data)
		if n < 0 {
			break
		}
		data = data[n:]
		if uint64(len(data)) < msgLen {
			break
		}
		msgData := data[:msgLen]
		data = data[msgLen:]

		key, val := parseStorageEntry(msgData)
		count++
		totalSize += len(val)
		if showKeys && (limit <= 0 || printed < limit) {
			keyCol.Printf("%s\n", key)
			if keys != nil {
				plaintext, err := decryptEntry(keys, key, val)
				if err != nil {
					dim.Printf("  [decrypt error: %v]\n", err)
				} else {
					printValue(plaintext, maxValueLen, "  ")
				}
			}
			printed++
			if limit > 0 && printed >= limit {
				dim.Printf("\n  [output limited to %d entries, continuing count...]\n", limit)
			}
		}
	}
	return
}
