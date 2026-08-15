package cmd

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/vadimzharov/pdbackup/internal/config"
	"github.com/vadimzharov/pdbackup/internal/kopia"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup data to S3 (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.ValidateForBackup(); err != nil {
			return err
		}

		if err := kopia.New(cfg).Backup(); err != nil {
			return err
		}

		slog.Info("backup completed successfully")
		return nil
	},
}
