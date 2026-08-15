package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/vadimzharov/pdbackup/internal/config"
	"github.com/vadimzharov/pdbackup/internal/kopia"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run periodic backups (for sidecar container)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.ValidateForBackup(); err != nil {
			return err
		}

		runner := kopia.New(cfg)
		slog.Info("starting backup daemon", "interval", cfg.BackupInterval, "source", cfg.SourceDir)

		runBackup := func() error {
			slog.Info("starting backup", "source", cfg.SourceDir)
			if err := runner.Backup(); err != nil {
				slog.Error("backup failed", "error", err)
				return err
			}
			slog.Info("backup completed", "source", cfg.SourceDir)
			return nil
		}

		// First backup immediately on start.
		if err := runBackup(); err != nil {
			return err
		}

		ticker := time.NewTicker(cfg.BackupInterval)
		defer ticker.Stop()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

		for {
			select {
			case <-ticker.C:
				if err := runBackup(); err != nil {
					return err
				}
			case sig := <-sigCh:
				slog.Info("received signal, shutting down", "signal", sig)
				return nil
			}
		}
	},
}
