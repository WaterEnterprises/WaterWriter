package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/WaterEnterprises/WaterWriter/internal/tui"
)

var openCmd = &cobra.Command{
	Use:   "open <project-name>",
	Short: "Open and resume an existing book project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		logger, llmClient, ag, err := initApp()
		if logger != nil {
			defer logger.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		if err := requireLLM(llmClient); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		project, err := ag.DB.GetProject(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error opening project:", err)
			os.Exit(1)
		}

		model := tui.NewModel(ag, project, logger)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	},
}
