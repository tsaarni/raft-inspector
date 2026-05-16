package main

import (
	"fmt"
	"os"
	"os/signal"
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

	var dataDir string
	var maxValueLen int

	root := &cobra.Command{
		Use:   "raft-inspector",
		Short: "Inspect OpenBao/Vault raft storage",
	}
	root.PersistentFlags().StringVarP(&dataDir, "data-dir", "d", "", "Path to the OpenBao/Vault data directory")
	root.PersistentFlags().IntVar(&maxValueLen, "max-value-length", 256, "Max bytes of decrypted value to display (0=unlimited)")

	requireDataDir := func(cmd *cobra.Command, args []string) error {
		if dataDir == "" {
			return fmt.Errorf("required flag \"data-dir\" not set")
		}
		return nil
	}

	statusCmd := newStatusCmd(&dataDir)
	statusCmd.PreRunE = requireDataDir
	logCmd := newLogCmd(&dataDir, &maxValueLen)
	logCmd.PreRunE = requireDataDir
	fsmCmd := newFsmCmd(&dataDir, &maxValueLen)
	fsmCmd.PreRunE = requireDataDir

	root.AddCommand(statusCmd)
	root.AddCommand(logCmd)
	root.AddCommand(fsmCmd)
	root.AddCommand(newSnapshotCmd(&maxValueLen))

	if err := root.Execute(); err != nil {
		cleanupTempFiles()
		os.Exit(1)
	}
	cleanupTempFiles()
}

func newStatusCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show raft and FSM health overview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStatus(*dataDir)
		},
	}
}

func newLogCmd(dataDir *string, maxValueLen *int) *cobra.Command {
	logCmd := &cobra.Command{
		Use:   "log [index]",
		Short: "List or inspect raft log entries",
		Args:  cobra.MaximumNArgs(1),
	}
	var logN uint64
	var logStats bool
	var logInitFile string
	logCmd.Flags().Uint64VarP(&logN, "count", "n", 0, "Show last N entries")
	logCmd.Flags().BoolVar(&logStats, "stats", false, "Show log statistics and hot keys")
	logCmd.Flags().StringVar(&logInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	logCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdLog(*dataDir, args, logN, logStats, logInitFile, *maxValueLen)
	}
	return logCmd
}

func newFsmCmd(dataDir *string, maxValueLen *int) *cobra.Command {
	fsmCmd := &cobra.Command{
		Use:   "fsm",
		Short: "Inspect the FSM (vault.db) key store",
		Args:  cobra.NoArgs,
	}
	var fsmPrefix string
	var fsmInitFile string
	var fsmLimit int
	fsmCmd.Flags().StringVar(&fsmPrefix, "prefix", "", "List keys matching prefix")
	fsmCmd.Flags().StringVar(&fsmInitFile, "unseal-key-file", "", "Path to unseal key JSON file (enables decryption)")
	fsmCmd.Flags().IntVar(&fsmLimit, "limit", 0, "Max number of keys to display (0=unlimited)")
	fsmCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdFsm(*dataDir, fsmPrefix, fsmInitFile, *maxValueLen, fsmLimit)
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

func cmdLog(dataDir string, args []string, n uint64, stats bool, initFile string, maxValueLen int) error {
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
		var index uint64
		if _, err := fmt.Sscanf(args[0], "%d", &index); err != nil {
			return fmt.Errorf("invalid index: %s", args[0])
		}
		return cmdLogSingle(dbPath, index, keys, maxValueLen)
	}
	return cmdLogList(dbPath, n, keys, maxValueLen)
}
