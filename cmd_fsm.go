package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	humanize "github.com/dustin/go-humanize"
	bolt "go.etcd.io/bbolt"
)

func cmdFsm(dataDir string, prefix string, initFile string, maxValueLen int, limit int) error {
	db, tmpPath, err := openVaultDB(dataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	defer os.Remove(tmpPath)

	var keys map[uint32][]byte
	if initFile != "" {
		rootKey, err := loadRootKey(initFile)
		if err != nil {
			return fmt.Errorf("loading root key: %w", err)
		}
		keys, err = loadKeyring(rootKey, db)
		if err != nil {
			return fmt.Errorf("loading keyring: %w", err)
		}
	}

	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("data"))
		if b == nil {
			fmt.Fprintf(os.Stderr, "data bucket not found\n")
			return nil
		}

		if prefix != "" {
			header.Printf("─── Keys matching prefix: %s ───\n", prefix)
			printed := 0
			b.ForEach(func(k, v []byte) error {
				if limit > 0 && printed >= limit {
					return nil
				}
				if strings.HasPrefix(string(k), prefix) {
					keyCol.Printf("%s", string(k))
					dim.Printf("  (%s)\n", humanize.Bytes(uint64(len(v))))
					if keys != nil {
						plaintext, err := decryptEntry(keys, string(k), v)
						if err != nil {
							dim.Printf("  [decrypt error: %v]\n", err)
						} else {
							printValue(plaintext, maxValueLen, "  ")
						}
					}
					printed++
				}
				return nil
			})
			if limit > 0 && printed >= limit {
				dim.Printf("\n  [output limited to %d entries]\n", limit)
			}
		} else {
			counts := map[string]int{}
			total := 0
			type keySize struct {
				key  string
				size int
			}
			var largest []keySize
			b.ForEach(func(k, v []byte) error {
				total++
				key := string(k)
				seg := key
				if idx := strings.Index(key, "/"); idx >= 0 {
					seg = key[:idx]
				}
				counts[seg]++
				ks := keySize{key, len(v)}
				if len(largest) < 10 {
					largest = append(largest, ks)
					sort.Slice(largest, func(i, j int) bool { return largest[i].size > largest[j].size })
				} else if len(v) > largest[9].size {
					largest[9] = ks
					sort.Slice(largest, func(i, j int) bool { return largest[i].size > largest[j].size })
				}
				return nil
			})
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
		}
		return nil
	})

	fmt.Println()
	dim.Println("  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]")
	if prefix != "" {
		dim.Println("  Size shown after each key is the encrypted (ciphertext) size, not the plaintext size. [vault.db]")
	} else {
		dim.Println("  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]")
		dim.Println("  Largest keys shows top 10 entries by encrypted value size. [vault.db]")
	}
	if keys != nil {
		dim.Println("  --unseal-key-file decrypts values using the keyring derived from the unseal key. [vault.db]")
	}
	return nil
}
