package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

type selectorKind int

const (
	selAll   selectorKind = iota // no selector: show all
	selIndex                     // single index
	selRange                     // index range start..end
	selTail                      // ~N last entries
	selDate                      // date range since..until
)

type selector struct {
	kind       selectorKind
	index      uint64    // selIndex
	start, end uint64    // selRange
	tail       uint64    // selTail
	since      time.Time // selDate (zero = open-ended)
	until      time.Time // selDate (zero = open-ended)
}

func parseSelector(s string) (selector, error) {
	if len(s) > 1 && s[0] == '~' {
		var n uint64
		if _, err := fmt.Sscanf(s[1:], "%d", &n); err == nil {
			return selector{kind: selTail, tail: n}, nil
		}
		return selector{}, fmt.Errorf("invalid tail selector: %s", s)
	}
	if parts := strings.SplitN(s, "..", 2); len(parts) == 2 {
		if looksLikeDate(parts[0]) || looksLikeDate(parts[1]) {
			return parseDateRange(parts[0], parts[1])
		}
		if parts[0] == "" || parts[1] == "" {
			return selector{}, fmt.Errorf("open-ended ranges require date format (YYYY-MM-DD): %s", s)
		}
		var a, b uint64
		if _, err := fmt.Sscanf(parts[0], "%d", &a); err != nil {
			return selector{}, fmt.Errorf("invalid range start: %s", parts[0])
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &b); err != nil {
			return selector{}, fmt.Errorf("invalid range end: %s", parts[1])
		}
		return selector{kind: selRange, start: a, end: b}, nil
	}
	var idx uint64
	if _, err := fmt.Sscanf(s, "%d", &idx); err == nil {
		return selector{kind: selIndex, index: idx}, nil
	}
	return selector{}, fmt.Errorf("invalid selector: %s", s)
}

func looksLikeDate(s string) bool {
	if s == "" {
		return false
	}
	// Must start with a 4-digit year to be considered a date.
	if len(s) < 10 {
		return false
	}
	return s[4] == '-' && s[7] == '-'
}

func parseDateRange(a, b string) (selector, error) {
	var sel selector
	sel.kind = selDate
	if a != "" {
		t, err := parseTime(a)
		if err != nil {
			return selector{}, fmt.Errorf("invalid start date: %w", err)
		}
		sel.since = t
	}
	if b != "" {
		t, err := parseTime(b)
		if err != nil {
			return selector{}, fmt.Errorf("invalid end date: %w", err)
		}
		sel.until = t
	}
	return sel, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q (expected RFC3339 or YYYY-MM-DD)", s)
}

func main() {
	// Clean up temp files on signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cleanupTempFiles()
		os.Exit(1)
	}()

	var maxValueLen int

	root := &cobra.Command{
		Use:   "raft-inspector",
		Short: "Inspect OpenBao raft storage",
	}
	root.PersistentFlags().IntVar(&maxValueLen, "max-value-length", 256, "Max bytes of decrypted value to display (0=unlimited)")

	root.AddCommand(newStatusCmd())
	root.AddCommand(newLogCmd(&maxValueLen))
	root.AddCommand(newFsmCmd(&maxValueLen))
	root.AddCommand(newSnapshotCmd(&maxValueLen))

	if err := root.Execute(); err != nil {
		cleanupTempFiles()
		os.Exit(1)
	}
	cleanupTempFiles()
}

func newStatusCmd() *cobra.Command {
	var dataDir, raftDB, vaultDB string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show raft and FSM health overview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raftPath, vaultPath, err := resolvePaths(dataDir, raftDB, vaultDB)
			if err != nil {
				return err
			}
			return cmdStatus(raftPath, vaultPath)
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Path to OpenBao data directory")
	cmd.Flags().StringVar(&raftDB, "raft-db", "", "Path to raft.db file")
	cmd.Flags().StringVar(&vaultDB, "vault-db", "", "Path to vault.db file")
	return cmd
}

func newLogCmd(maxValueLen *int) *cobra.Command {
	var dataDir, raftDB, vaultDB string
	var logStats bool
	var logInitFile, logUnsealKey string
	logCmd := &cobra.Command{
		Use:   "log [range]",
		Short: "List or inspect raft log entries",
		Long: `List or inspect raft log entries.

Range argument selects which entries to display:
  5                              single entry at index 5
  1..10                          entries from index 1 to 10
  ~10                            last 10 entries
  2026-06-15..2026-06-16         entries within date range
  2026-06-15T10:00:00Z..         entries from date to end
  ..2026-06-16                   entries from start to date
  (none)                         all entries`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rootKey, err := resolveRootKey(logInitFile, logUnsealKey)
			if err != nil {
				return err
			}
			raftPath, vaultPath, err := resolvePaths(dataDir, raftDB, vaultDB)
			if err != nil {
				return err
			}
			return cmdLog(raftPath, vaultPath, args, logStats, rootKey, *maxValueLen)
		},
	}
	logCmd.Flags().StringVar(&dataDir, "data-dir", "", "Path to OpenBao data directory")
	logCmd.Flags().StringVar(&raftDB, "raft-db", "", "Path to raft.db file")
	logCmd.Flags().StringVar(&vaultDB, "vault-db", "", "Path to vault.db file")
	logCmd.Flags().BoolVar(&logStats, "stats", false, "Show log statistics and hot keys")
	logCmd.Flags().StringVar(&logInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	logCmd.Flags().StringVar(&logUnsealKey, "unseal-key", "", "Unseal key as hex or base64 string (enables decryption)")
	return logCmd
}

func newFsmCmd(maxValueLen *int) *cobra.Command {
	var dataDir, vaultDB string
	var fsmPrefix, fsmInitFile, fsmUnsealKey string
	var fsmLimit int
	fsmCmd := &cobra.Command{
		Use:   "fsm",
		Short: "Inspect the FSM (vault.db) key store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rootKey, err := resolveRootKey(fsmInitFile, fsmUnsealKey)
			if err != nil {
				return err
			}
			_, vaultPath, err := resolvePaths(dataDir, "", vaultDB)
			if err != nil {
				return err
			}
			return cmdFsm(vaultPath, fsmPrefix, rootKey, *maxValueLen, fsmLimit)
		},
	}
	fsmCmd.Flags().StringVar(&dataDir, "data-dir", "", "Path to OpenBao data directory")
	fsmCmd.Flags().StringVar(&vaultDB, "vault-db", "", "Path to vault.db file")
	fsmCmd.Flags().StringVar(&fsmPrefix, "prefix", "", "List keys matching prefix")
	fsmCmd.Flags().StringVar(&fsmInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	fsmCmd.Flags().StringVar(&fsmUnsealKey, "unseal-key", "", "Unseal key as hex or base64 string (enables decryption)")
	fsmCmd.Flags().IntVar(&fsmLimit, "limit", 0, "Max number of keys to display (0=unlimited)")
	return fsmCmd
}

func newSnapshotCmd(maxValueLen *int) *cobra.Command {
	var snapPrefix, snapInitFile, snapUnsealKey string
	var snapLimit int
	snapshotCmd := &cobra.Command{
		Use:   "snapshot <file>",
		Short: "Inspect an external snapshot archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rootKey, err := resolveRootKey(snapInitFile, snapUnsealKey)
			if err != nil {
				return err
			}
			return cmdSnapshot(args[0], snapPrefix, rootKey, *maxValueLen, snapLimit)
		},
	}
	snapshotCmd.Flags().StringVar(&snapPrefix, "prefix", "", "List keys matching prefix")
	snapshotCmd.Flags().StringVar(&snapInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	snapshotCmd.Flags().StringVar(&snapUnsealKey, "unseal-key", "", "Unseal key as hex or base64 string (enables decryption)")
	snapshotCmd.Flags().IntVar(&snapLimit, "limit", 0, "Max number of keys to display (0=unlimited)")
	return snapshotCmd
}

func cmdLog(raftPath, vaultPath string, args []string, stats bool, rootKey []byte, maxValueLen int) error {
	var keys map[uint32][]byte
	if rootKey != nil {
		db, tmpPath, err := openVaultDB(vaultPath)
		if err != nil {
			return err
		}
		defer db.Close()
		defer os.Remove(tmpPath)
		keys, err = loadKeyring(rootKey, db)
		if err != nil {
			return fmt.Errorf("loading keyring: %w", err)
		}
	}

	var sel selector
	if len(args) == 1 {
		var err error
		sel, err = parseSelector(args[0])
		if err != nil {
			return err
		}
	}

	if stats {
		return cmdLogStats(raftPath, sel)
	}
	if sel.kind == selIndex {
		return cmdLogSingle(raftPath, sel.index, keys, maxValueLen)
	}
	return cmdLogList(raftPath, sel, keys, maxValueLen)
}

func resolveRootKey(initFile, unsealKey string) ([]byte, error) {
	if initFile != "" && unsealKey != "" {
		return nil, fmt.Errorf("cannot use both --unseal-key-file and --unseal-key")
	}
	if initFile != "" {
		return loadRootKey(initFile)
	}
	if unsealKey != "" {
		return decodeKeyValue(unsealKey)
	}
	return nil, nil
}

// resolvePaths resolves raft.db and vault.db paths.
// Use --data-dir for standard OpenBao layout, or --raft-db/--vault-db for direct file paths.
func resolvePaths(dataDir, raftDB, vaultDB string) (raftPath, vaultPath string, err error) {
	hasDir := dataDir != ""
	hasFiles := raftDB != "" || vaultDB != ""

	if hasDir && hasFiles {
		return "", "", fmt.Errorf("cannot use --data-dir together with --raft-db/--vault-db")
	}
	if !hasDir && !hasFiles {
		return "", "", fmt.Errorf("provide --data-dir or --raft-db/--vault-db")
	}

	if hasDir {
		return filepath.Join(dataDir, "raft", "raft.db"), filepath.Join(dataDir, "vault.db"), nil
	}
	return raftDB, vaultDB, nil
}
