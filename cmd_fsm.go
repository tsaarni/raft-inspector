package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func cmdFsm(dataDir string, prefix string, top bool, decrypt bool, initFile string, maxValueLen int, limit int) error {
	db, tmpPath, err := openVaultDB(dataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	defer os.Remove(tmpPath)

	var keys map[uint32][]byte
	if decrypt {
		if initFile == "" {
			return fmt.Errorf("--decrypt requires --unseal-key-file")
		}
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

		if top {
			counts := map[string]int{}
			b.ForEach(func(k, v []byte) error {
				key := string(k)
				seg := key
				if idx := strings.Index(key, "/"); idx >= 0 {
					seg = key[:idx]
				}
				counts[seg]++
				return nil
			})
			type kv struct {
				k string
				v int
			}
			var sorted []kv
			for k, v := range counts {
				sorted = append(sorted, kv{k, v})
			}
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
			header.Println("─── Top-level Key Segments ───")
			for _, item := range sorted {
				label.Printf("  %-40s", item.k)
				value.Printf("%d\n", item.v)
			}
		} else if prefix != "" {
			header.Printf("─── Keys matching prefix: %s ───\n", prefix)
			printed := 0
			b.ForEach(func(k, v []byte) error {
				if limit > 0 && printed >= limit {
					return nil
				}
				if strings.HasPrefix(string(k), prefix) {
					keyCol.Printf("%s\n", string(k))
					if decrypt {
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
			count := 0
			b.ForEach(func(k, v []byte) error {
				count++
				return nil
			})
			label.Printf("Total keys in data bucket: ")
			value.Printf("%d\n", count)
		}
		return nil
	})

	fmt.Println()
	dim.Println("  Keys are plaintext storage paths from the vault.db data bucket; values are AES-GCM encrypted. [vault.db]")
	dim.Println("  Top-level segments correspond to subsystems (core/, sys/, logical/) and their key counts. [vault.db]")
	if decrypt {
		dim.Println("  --decrypt decrypts values using the keyring derived from the unseal key. [vault.db]")
	}
	return nil
}
