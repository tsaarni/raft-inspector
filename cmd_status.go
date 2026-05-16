package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func cmdStatus(dataDir string) error {
	raftPath := filepath.Join(dataDir, "raft", "raft.db")

	store, raftTmp, err := openStore(raftPath)
	if err != nil {
		return err
	}
	defer os.Remove(raftTmp)

	header.Println("─── raft/raft.db stable store ───")
	term, _ := store.GetUint64([]byte("CurrentTerm"))
	label.Printf("  %-20s", "Current Term:")
	value.Printf("%d\n", term)

	first, _ := store.FirstIndex()
	last, _ := store.LastIndex()
	label.Printf("  %-20s", "First Log Index:")
	value.Printf("%d\n", first)
	label.Printf("  %-20s", "Last Log Index:")
	value.Printf("%d\n", last)
	label.Printf("  %-20s", "Entry Count:")
	value.Printf("%d\n", last-first+1)

	cand, _ := store.Get([]byte("LastVoteCand"))
	label.Printf("  %-20s", "Last Vote Cand:")
	value.Printf("%s\n", cand)

	termBytes, _ := store.Get([]byte("LastVoteTerm"))
	if len(termBytes) == 8 {
		label.Printf("  %-20s", "Last Vote Term:")
		value.Printf("%d\n", binary.BigEndian.Uint64(termBytes))
	}

	// Close raftboltdb store so we can reopen as raw bolt for stats.
	store.Close()

	// Vault.db FSM state
	vaultPath := filepath.Join(dataDir, "vault.db")
	fi, statErr := os.Stat(vaultPath)
	if statErr != nil || fi.Size() == 0 {
		fmt.Fprintf(os.Stderr, "\nvault.db is empty or missing (node may not have been initialized yet)\n")
		return nil
	}

	db, vaultTmp, err := openVaultDB(dataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	defer os.Remove(vaultTmp)

	fmt.Println()
	header.Println("─── vault.db config bucket ───")

	var appliedIndex uint64
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("config"))
		if b == nil {
			fmt.Fprintf(os.Stderr, "  config bucket not found\n")
			return nil
		}

		if data := b.Get([]byte("latest_indexes")); data != nil {
			var iv IndexValue
			if err := proto.Unmarshal(data, &iv); err == nil {
				label.Printf("  %-20s", "Applied Index:")
				value.Printf("%d", iv.Index)
				dim.Printf("  (config/latest_indexes)\n")
				label.Printf("  %-20s", "Applied Term:")
				value.Printf("%d", iv.Term)
				dim.Printf("  (config/latest_indexes)\n")
				appliedIndex = iv.Index
			}
		}

		if data := b.Get([]byte("latest_config")); data != nil {
			var cv ConfigurationValue
			if err := proto.Unmarshal(data, &cv); err == nil {
				label.Printf("  %-20s", "Config Index:")
				value.Printf("%d", cv.Index)
				dim.Printf("  (config/latest_config)\n")
				label.Printf("  %-20s", "Servers:")
				dim.Printf("  (config/latest_config)\n")
				for _, s := range cv.Servers {
					value.Printf("    - %s (%s) %s\n", s.Id, s.Address, suffrageName(s.Suffrage))
				}
			}
		}

		if data := b.Get([]byte("local_node_config")); data != nil {
			var lnc LocalNodeConfigValue
			if err := proto.Unmarshal(data, &lnc); err == nil {
				label.Printf("  %-20s", "Desired Suffrage:")
				value.Printf("%s", lnc.DesiredSuffrage)
				dim.Printf("  (config/local_node_config)\n")
			}
		}

		return nil
	})

	fmt.Println()
	header.Println("─── Computed ───")
	label.Printf("  %-20s", "Unapplied Entries:")
	value.Printf("%d\n", last-appliedIndex)
	label.Printf("  %-20s", "Trailing Entries:")
	value.Printf("%d\n", appliedIndex-first)
	label.Printf("  %-20s", "Snapshot Index:")
	value.Printf("%d\n", first-1)

	// BoltDB stats: reopen the raft temp copy as raw bolt.
	fmt.Println()
	header.Println("─── BoltDB Stats: raft/raft.db ───")
	raftDB, err := bolt.Open(raftTmp, 0600, &bolt.Options{ReadOnly: true, PreLoadFreelist: true})
	if err == nil {
		printBoltStats(raftDB, raftPath)
		raftDB.Close()
	}

	fmt.Println()
	header.Println("─── BoltDB Stats: vault.db ───")
	printBoltStats(db, vaultPath)

	fmt.Println()
	dim.Println("  Current Term       Raft election epoch; increments each time a new leader election occurs. [raft/raft.db]")
	dim.Println("  First Log Index    Oldest log entry still retained in the log store. [raft/raft.db]")
	dim.Println("  Last Log Index     Most recent log entry written to the log store. [raft/raft.db]")
	dim.Println("  Entry Count        Number of log entries currently retained (last - first + 1). [raft/raft.db]")
	dim.Println("  Last Vote Cand     Node this server last voted for in a leader election. [raft/raft.db]")
	dim.Println("  Last Vote Term     Term in which the last vote was cast. [raft/raft.db]")
	dim.Println("  Applied Index      Last log entry applied to the FSM (state machine). [vault.db]")
	dim.Println("  Applied Term       Term of the last applied log entry. [vault.db]")
	dim.Println("  Config Index       Log index at which the current cluster membership was committed. [vault.db]")
	dim.Println("  Servers            Cluster members: voter=participates in elections/quorum, nonvoter=replica only. [vault.db]")
	dim.Println("  Desired Suffrage   Role this node wants to have in the cluster (voter or nonvoter). [vault.db]")
	dim.Println("  Unapplied Entries  Log entries not yet applied to the FSM; should be 0 on a healthy node. [computed]")
	dim.Println("  Trailing Entries   Applied entries kept in the log for follower catch-up without full snapshot. [computed]")
	dim.Println("  Snapshot Index     Highest index that was truncated; entries at or below this were compacted away. [computed]")
	dim.Println("  File Size          Total size of the BoltDB file on disk. [os.Stat]")
	dim.Println("  DB Logical Size    Pages allocated by BoltDB (file may be larger due to preallocation). [bolt.Tx.Size]")
	dim.Println("  Page Size          BoltDB page size; all allocations are in multiples of this. [bolt.DB.Info]")
	dim.Println("  Free Pages         Pages released by deletes but not yet returned to OS; reused for future writes. [bolt.DB.Stats]")
	dim.Println("  Pending Pages      Pages freed in current transaction, not yet available for reuse. [bolt.DB.Stats]")
	dim.Println("  Freelist In-Use    Bytes used by BoltDB's internal freelist tracking structure. [bolt.DB.Stats]")
	dim.Println("  Space Efficiency   Percentage of file occupied by live data (excludes free pages and preallocation). [computed]")
	dim.Println("  Bucket <name>      Per-bucket B+tree: key count, depth, branch/leaf page utilization %. [bolt.Bucket.Stats]")
	dim.Println("  Integrity Check    Verifies all pages are reachable or freed, no double refs. [bolt.Tx.Check]")
	return nil
}

func printBoltStats(db *bolt.DB, filePath string) {
	stats := db.Stats()
	fi, _ := os.Stat(filePath)
	var fileSize int64
	if fi != nil {
		fileSize = fi.Size()
	}
	var dbSize int64
	var pageSize int
	db.View(func(tx *bolt.Tx) error {
		dbSize = tx.Size()
		pageSize = db.Info().PageSize
		return nil
	})
	freeBytes := stats.FreePageN * pageSize

	label.Printf("  %-20s", "File Size:")
	value.Printf("%d bytes (%.1f MB)\n", fileSize, float64(fileSize)/1024/1024)
	label.Printf("  %-20s", "DB Logical Size:")
	value.Printf("%d bytes (%.1f MB)\n", dbSize, float64(dbSize)/1024/1024)
	label.Printf("  %-20s", "Page Size:")
	value.Printf("%d bytes\n", pageSize)
	label.Printf("  %-20s", "Free Pages:")
	if fileSize > 0 {
		value.Printf("%d (%d bytes, %.1f%%)\n", stats.FreePageN, freeBytes, float64(freeBytes)/float64(fileSize)*100)
	} else {
		value.Printf("%d (%d bytes)\n", stats.FreePageN, freeBytes)
	}
	label.Printf("  %-20s", "Pending Pages:")
	value.Printf("%d\n", stats.PendingPageN)
	label.Printf("  %-20s", "Freelist In-Use:")
	value.Printf("%d bytes\n", stats.FreelistInuse)
	if fileSize > 0 {
		liveBytes := dbSize - int64(freeBytes)
		label.Printf("  %-20s", "Space Efficiency:")
		value.Printf("%.1f%% (%.1f MB live data)\n",
			float64(liveBytes)/float64(fileSize)*100, float64(liveBytes)/1024/1024)
	}

	// Per-bucket B+tree stats.
	db.View(func(tx *bolt.Tx) error {
		var agg bolt.BucketStats
		var count int
		tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			bs := b.Stats()
			agg.Add(bs)
			count++
			branchPct := 0
			if bs.BranchAlloc > 0 {
				branchPct = int(float32(bs.BranchInuse) * 100 / float32(bs.BranchAlloc))
			}
			leafPct := 0
			if bs.LeafAlloc > 0 {
				leafPct = int(float32(bs.LeafInuse) * 100 / float32(bs.LeafAlloc))
			}
			label.Printf("  %-20s", fmt.Sprintf("Bucket %q:", name))
			value.Printf("%d keys, depth %d, branch %d%% leaf %d%% utilization\n",
				bs.KeyN, bs.Depth, branchPct, leafPct)
			return nil
		})
		if count > 1 {
			branchPct := 0
			if agg.BranchAlloc > 0 {
				branchPct = int(float32(agg.BranchInuse) * 100 / float32(agg.BranchAlloc))
			}
			leafPct := 0
			if agg.LeafAlloc > 0 {
				leafPct = int(float32(agg.LeafInuse) * 100 / float32(agg.LeafAlloc))
			}
			label.Printf("  %-20s", "Total:")
			value.Printf("%d keys, branch %d%% leaf %d%% utilization\n",
				agg.KeyN, branchPct, leafPct)
		}
		return nil
	})

	// Integrity check.
	var checkErrs int
	db.View(func(tx *bolt.Tx) error {
		for range tx.Check() {
			checkErrs++
		}
		return nil
	})
	label.Printf("  %-20s", "Integrity Check:")
	if checkErrs == 0 {
		value.Printf("OK\n")
	} else {
		warn.Printf("FAILED (%d errors)\n", checkErrs)
	}
}
