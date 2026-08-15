package cmd

import (
	"github.com/spf13/cobra"
	"github.com/vadimzharov/pdbackup/internal/config"
	"github.com/vadimzharov/pdbackup/internal/kopia"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available snapshots in the repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.ValidateForList(); err != nil {
			return err
		}
		return kopia.New(cfg).List()
	},
}
