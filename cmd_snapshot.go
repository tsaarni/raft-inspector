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
	"sort"
	"strings"

	humanize "github.com/dustin/go-humanize"
	"github.com/fatih/color"
	"google.golang.org/protobuf/encoding/protowire"
)

func cmdSnapshot(file string, prefix string, initFile string, maxValueLen int, limit int) error {
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

	var metaJSON []byte
	var sha256sums []byte
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
			if err := processStateBin(tr, checksums, prefix, initFile, maxValueLen, limit, metaJSON, sha256sums); err != nil {
				return err
			}
			return nil
		default:
			data, _ := io.ReadAll(tr)
			h := sha256.Sum256(data)
			checksums[name] = hex.EncodeToString(h[:])
		}
	}

	printSnapshotMeta(metaJSON)
	printChecksumVerification(sha256sums, checksums)
	return nil
}

func processStateBin(r io.Reader, checksums map[string]string, prefix string, initFile string, maxValueLen int, limit int, metaJSON, sha256sums []byte) error {
	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	data, err := io.ReadAll(tee)
	if err != nil {
		return fmt.Errorf("reading state.bin: %w", err)
	}
	checksums["state.bin"] = hex.EncodeToString(hasher.Sum(nil))

	var keys map[uint32][]byte
	if initFile != "" {
		rootKey, err := loadRootKey(initFile)
		if err != nil {
			return fmt.Errorf("loading root key: %w", err)
		}
		keys, err = loadKeyringFromStateBin(rootKey, data)
		if err != nil {
			return fmt.Errorf("loading keyring from snapshot: %w", err)
		}
	}

	if prefix != "" {
		header.Printf("─── Keys matching prefix: %s ───\n", prefix)
		printed := 0
		parseStateBinFunc(data, func(key string, val []byte) {
			if limit > 0 && printed >= limit {
				return
			}
			if strings.HasPrefix(key, prefix) {
				keyCol.Printf("%s", key)
				dim.Printf("  (%s)\n", humanize.Bytes(uint64(len(val))))
				if keys != nil {
					plaintext, err := decryptEntry(keys, key, val)
					if err != nil {
						dim.Printf("  [decrypt error: %v]\n", err)
					} else {
						printValue(plaintext, maxValueLen, "  ")
					}
				}
				printed++
			}
		})
		if limit > 0 && printed >= limit {
			dim.Printf("\n  [output limited to %d entries]\n", limit)
		}
		fmt.Println()
		dim.Println("  Keys are plaintext storage paths from the snapshot state; values are AES-GCM encrypted. [state.bin]")
		dim.Println("  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [state.bin]")
		if keys != nil {
			dim.Println("  --unseal-key-file decrypts values using the keyring derived from the unseal key. [state.bin]")
		}
	} else {
		printSnapshotMeta(metaJSON)
		printChecksumVerification(sha256sums, checksums)

		counts := map[string]int{}
		total := 0
		type keySize struct {
			key  string
			size int
		}
		var largest []keySize
		parseStateBinFunc(data, func(key string, val []byte) {
			total++
			seg := key
			if idx := strings.Index(key, "/"); idx >= 0 {
				seg = key[:idx]
			}
			counts[seg]++
			ks := keySize{key, len(val)}
			if len(largest) < 10 {
				largest = append(largest, ks)
				sort.Slice(largest, func(i, j int) bool { return largest[i].size > largest[j].size })
			} else if len(val) > largest[9].size {
				largest[9] = ks
				sort.Slice(largest, func(i, j int) bool { return largest[i].size > largest[j].size })
			}
		})

		fmt.Println()
		header.Println("─── State Data ───")
		label.Printf("  %-16s", "Total Keys:")
		value.Printf("%d\n", total)
		fmt.Println()
		header.Println("─── Top-level Key Segments ───")
		type kv struct {
			k string
			v int
		}
		var sorted []kv
		for k, v := range counts {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
		for _, item := range sorted {
			label.Printf("  %-16s", item.k)
			value.Printf("%d\n", item.v)
		}
		fmt.Println()
		header.Println("─── Largest Keys ───")
		for _, item := range largest {
			value.Printf("  %9s  ", humanize.Bytes(uint64(item.size)))
			keyCol.Printf("%s\n", item.key)
		}

		fmt.Println()
		dim.Println("  Index            Raft log index at which this snapshot was taken. [meta.json]")
		dim.Println("  Term             Raft term at the time of snapshot. [meta.json]")
		dim.Println("  Servers          Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [meta.json]")
		dim.Println("  Checksums        SHA-256 integrity verification of archive contents. [SHA256SUMS]")
		dim.Println("  Total Keys       Number of key/value entries in the FSM state dump. [state.bin]")
		dim.Println("  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [state.bin]")
		dim.Println("  Largest keys shows top 10 entries by encrypted value size. [state.bin]")
	}
	return nil
}

// parseStateBinFunc iterates over all key/value entries in state.bin data, calling fn for each.
func parseStateBinFunc(data []byte, fn func(key string, val []byte)) {
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
		fn(key, val)
	}
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
