package cmd

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/vadimzharov/pdbackup/internal/config"
	"github.com/vadimzharov/pdbackup/internal/kopia"
)

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore latest snapshot from S3 (one-shot, for initContainer)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.ValidateForRestore(); err != nil {
			return err
		}

		if err := kopia.New(cfg).Restore(); err != nil {
			return err
		}

		slog.Info("restore completed successfully")
		return nil
	},
}
