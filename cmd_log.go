package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"
)

func cmdLogList(dbPath string, n uint64, keys map[uint32][]byte, maxValueLen int) error {
	store, tmpPath, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	defer os.Remove(tmpPath)

	first, _ := store.FirstIndex()
	last, _ := store.LastIndex()

	start := first
	if n > 0 && last-first+1 > n {
		start = last - n + 1
	}

	header.Printf("─── raft/raft.db logs bucket (entries %d to %d, showing %d to %d) ───\n\n", first, last, start, last)
	var refTime *time.Time
	for i := start; i <= last; i++ {
		var log raft.Log
		if err := store.GetLog(i, &log); err != nil {
			fmt.Printf("Index %d: error: %v\n", i, err)
			continue
		}
		if refTime == nil && !log.AppendedAt.IsZero() {
			t := log.AppendedAt
			refTime = &t
		}
		header.Printf("─── Index %d (raft/raft.db logs/%d) ───\n", log.Index, log.Index)
		printLog(&log, refTime, keys, maxValueLen)
		fmt.Println()
	}

	fmt.Println()
	printLogLegend()
	return nil
}

func cmdLogSingle(dbPath string, index uint64, keys map[uint32][]byte, maxValueLen int) error {
	store, tmpPath, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	defer os.Remove(tmpPath)

	var log raft.Log
	if err := store.GetLog(index, &log); err != nil {
		return fmt.Errorf("reading log index %d: %w", index, err)
	}
	printLog(&log, nil, keys, maxValueLen)

	fmt.Println()
	printLogLegend()
	return nil
}

func cmdLogStats(dbPath string) error {
	store, tmpPath, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	defer os.Remove(tmpPath)

	first, _ := store.FirstIndex()
	last, _ := store.LastIndex()

	opCounts := map[string]int{}
	keyCounts := map[string]int{}
	var totalSize, maxSize uint64
	var firstTime, lastTime string

	for i := first; i <= last; i++ {
		var log raft.Log
		if err := store.GetLog(i, &log); err != nil {
			continue
		}

		size := uint64(len(log.Data))
		totalSize += size
		if size > maxSize {
			maxSize = size
		}

		if i == first {
			firstTime = log.AppendedAt.String()
		}
		if i == last {
			lastTime = log.AppendedAt.String()
		}

		if log.Type == raft.LogCommand && len(log.Data) > 0 {
			var ld LogData
			if err := proto.Unmarshal(log.Data, &ld); err == nil {
				for _, op := range ld.Operations {
					opCounts[opName(op.OpType)]++
					keyCounts[op.Key]++
				}
			}
		}
	}

	entryCount := last - first + 1

	header.Println("─── Log Statistics ───")
	label.Printf("  %-20s", "Time Range:")
	value.Printf("%s → %s\n", firstTime, lastTime)
	label.Printf("  %-20s", "Entry Count:")
	value.Printf("%d\n", entryCount)
	label.Printf("  %-20s", "Total Size:")
	value.Printf("%s\n", humanize.Bytes(totalSize))
	label.Printf("  %-20s", "Average Size:")
	if entryCount > 0 {
		value.Printf("%s\n", humanize.Bytes(totalSize/entryCount))
	} else {
		value.Printf("0 B\n")
	}
	label.Printf("  %-20s", "Max Size:")
	value.Printf("%s\n", humanize.Bytes(maxSize))

	fmt.Println()
	header.Println("─── Operation Distribution ───")
	type kv struct {
		k string
		v int
	}
	var opSorted []kv
	for k, v := range opCounts {
		opSorted = append(opSorted, kv{k, v})
	}
	sort.Slice(opSorted, func(i, j int) bool { return opSorted[i].v > opSorted[j].v })
	for _, item := range opSorted {
		label.Printf("  %-20s", item.k)
		value.Printf("%d\n", item.v)
	}

	fmt.Println()
	header.Println("─── Hot Keys (top 10) ───")
	var keySorted []kv
	for k, v := range keyCounts {
		keySorted = append(keySorted, kv{k, v})
	}
	sort.Slice(keySorted, func(i, j int) bool { return keySorted[i].v > keySorted[j].v })
	limit := 10
	if len(keySorted) < limit {
		limit = len(keySorted)
	}
	for _, item := range keySorted[:limit] {
		keyCol.Printf("  %-60s", item.k)
		value.Printf("%d\n", item.v)
	}

	fmt.Println()
	dim.Println("  Time Range         Wall-clock range from oldest to newest log entry's AppendedAt timestamp. [raft/raft.db]")
	dim.Println("  Entry Count        Total number of log entries in the retained log. [raft/raft.db]")
	dim.Println("  Total/Avg/Max Size Byte sizes of log entry Data payloads (encrypted operations). [raft/raft.db]")
	dim.Println("  Op Distribution    Count of each operation type (put, delete, etc.) across all log entries. [raft/raft.db]")
	dim.Println("  Hot Keys           Storage paths most frequently written to; helps identify write-heavy workloads. [raft/raft.db]")
	return nil
}

func printLog(log *raft.Log, refTime *time.Time, keys map[uint32][]byte, maxValueLen int) {
	label.Printf("  %-12s", "Index:")
	value.Printf("%d\n", log.Index)
	label.Printf("  %-12s", "Term:")
	value.Printf("%d\n", log.Term)
	label.Printf("  %-12s", "Type:")
	value.Printf("%s\n", log.Type)
	label.Printf("  %-12s", "AppendedAt:")
	if refTime != nil && !log.AppendedAt.IsZero() {
		offset := log.AppendedAt.Sub(*refTime)
		value.Printf("%s  (+%s)\n", log.AppendedAt, offset.Round(time.Millisecond))
	} else {
		value.Printf("%s\n", log.AppendedAt)
	}

	if log.Type == raft.LogCommand && len(log.Data) > 0 {
		var ld LogData
		if err := proto.Unmarshal(log.Data, &ld); err == nil {
			label.Printf("  Operations:\n")
			for _, op := range ld.Operations {
				opColor.Printf("    [op=%d/%s] ", op.OpType, opName(op.OpType))
				keyCol.Printf("%s", op.Key)
				value.Printf("  (%s)\n", humanize.Bytes(uint64(len(op.Value))))
				if keys != nil && len(op.Value) > 0 && op.OpType == 2 {
					printDecryptedValue(keys, op.Key, op.Value, maxValueLen)
				}
			}
		} else {
			fmt.Printf("  Data: (protobuf decode error: %v)\n", err)
		}
	}
}

func printLogLegend() {
	dim.Println("  Index        Sequence number of this entry in the raft log; monotonically increasing. [raft/raft.db]")
	dim.Println("  Term         Election term when this entry was created by the leader. [raft/raft.db]")
	dim.Println("  Type         Entry type: LogCommand (data op), LogConfiguration (membership change), LogBarrier, LogNoop. [raft/raft.db]")
	dim.Println("  AppendedAt   Wall-clock time when the leader appended this entry to its log. [raft/raft.db]")
	dim.Println("  Operations   Decoded operations: op type (put/delete), storage key path, and encrypted value size. [raft/raft.db]")
}
