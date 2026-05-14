package cmd

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/migrate"
	"github.com/devenjarvis/refrain/internal/tui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "refrain",
	Short: "A terminal-native tool for orchestrating multiple Claude Code agents",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Skip migration for the hook subprocess: it runs once per Claude hook
		// event and must stay silent on stderr so it doesn't confuse Claude.
		if cmd.Name() == "hook" {
			return
		}
		if err := migrate.GlobalHome(); err != nil {
			fmt.Fprintf(os.Stderr, "refrain: %v\n", err)
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(tui.NewApp())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error running TUI: %w", err)
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
