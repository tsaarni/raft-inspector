package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

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
		Short: "Inspect OpenBao/Vault raft storage",
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
	return &cobra.Command{
		Use:   "status <data-dir>",
		Short: "Show raft and FSM health overview",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStatus(args[0])
		},
	}
}

func newLogCmd(maxValueLen *int) *cobra.Command {
	logCmd := &cobra.Command{
		Use:   "log <data-dir> [range]",
		Short: "List or inspect raft log entries",
		Long: `List or inspect raft log entries.

Range argument selects which entries to display:
  5       single entry at index 5
  1..10   entries from index 1 to 10
  ~10     last 10 entries
  (none)  all entries`,
		Args: cobra.RangeArgs(1, 2),
	}
	var logStats bool
	var logInitFile string
	logCmd.Flags().BoolVar(&logStats, "stats", false, "Show log statistics and hot keys")
	logCmd.Flags().StringVar(&logInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	logCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdLog(args[0], args[1:], logStats, logInitFile, *maxValueLen)
	}
	return logCmd
}

func newFsmCmd(maxValueLen *int) *cobra.Command {
	fsmCmd := &cobra.Command{
		Use:   "fsm <data-dir>",
		Short: "Inspect the FSM (vault.db) key store",
		Args:  cobra.ExactArgs(1),
	}
	var fsmPrefix string
	var fsmInitFile string
	var fsmLimit int
	fsmCmd.Flags().StringVar(&fsmPrefix, "prefix", "", "List keys matching prefix")
	fsmCmd.Flags().StringVar(&fsmInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	fsmCmd.Flags().IntVar(&fsmLimit, "limit", 0, "Max number of keys to display (0=unlimited)")
	fsmCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdFsm(args[0], fsmPrefix, fsmInitFile, *maxValueLen, fsmLimit)
	}
	return fsmCmd
}

func newSnapshotCmd(maxValueLen *int) *cobra.Command {
	snapshotCmd := &cobra.Command{
		Use:   "snapshot <file>",
		Short: "Inspect an external snapshot archive",
		Args:  cobra.ExactArgs(1),
	}
	var snapPrefix string
	var snapInitFile string
	var snapLimit int
	snapshotCmd.Flags().StringVar(&snapPrefix, "prefix", "", "List keys matching prefix")
	snapshotCmd.Flags().StringVar(&snapInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	snapshotCmd.Flags().IntVar(&snapLimit, "limit", 0, "Max number of keys to display (0=unlimited)")
	snapshotCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdSnapshot(args[0], snapPrefix, snapInitFile, *maxValueLen, snapLimit)
	}
	return snapshotCmd
}

func cmdLog(dataDir string, args []string, stats bool, initFile string, maxValueLen int) error {
	dbPath := fmt.Sprintf("%s/raft/raft.db", dataDir)
	var keys map[uint32][]byte
	if initFile != "" {
		rootKey, err := loadRootKey(initFile)
		if err != nil {
			return fmt.Errorf("loading root key: %w", err)
		}
		db, tmpPath, err := openVaultDB(dataDir)
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
	if stats {
		return cmdLogStats(dbPath)
	}
	if len(args) == 1 {
		arg := args[0]
		// Parse range: "1..10" or "~10" (last 10)
		if start, end, ok := parseRange(arg); ok {
			return cmdLogList(dbPath, start, end, keys, maxValueLen)
		}
		var index uint64
		if _, err := fmt.Sscanf(arg, "%d", &index); err != nil {
			return fmt.Errorf("invalid index or range: %s", arg)
		}
		return cmdLogSingle(dbPath, index, keys, maxValueLen)
	}
	return cmdLogList(dbPath, 0, 0, keys, maxValueLen)
}

// parseRange parses "START..END" or "~N" (last N entries).
// For "~N", start is returned as 0 to signal "last N" mode.
func parseRange(s string) (start, end uint64, ok bool) {
	// "~N" means last N entries
	if len(s) > 1 && s[0] == '~' {
		var n uint64
		if _, err := fmt.Sscanf(s[1:], "%d", &n); err == nil {
			return 0, n, true
		}
	}
	// "START..END"
	if parts := strings.SplitN(s, "..", 2); len(parts) == 2 {
		var a, b uint64
		if _, err := fmt.Sscanf(parts[0], "%d", &a); err == nil {
			if _, err := fmt.Sscanf(parts[1], "%d", &b); err == nil {
				return a, b, true
			}
		}
	}
	return 0, 0, false
}
