package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "pdbackup",
	Short: "Kubernetes pod backup and restore using Kopia",
	Long: `pdbackup backs up and restores Kubernetes pod data to/from S3 using Kopia.

Run as an initContainer with the 'restore' command to seed a volume before the
main container starts. Run as a sidecar with the 'daemon' command for periodic
backups.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		opts := &slog.HandlerOptions{Level: slog.LevelInfo}
		if os.Getenv("PDBACKUP_DEBUG") != "" {
			opts.Level = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(listCmd)
}
