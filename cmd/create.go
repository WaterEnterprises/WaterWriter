package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/WaterEnterprises/WaterWriter/internal/tui"
)

var createCmd = &cobra.Command{
	Use:   "create <project-name>",
	Short: "Create a new book project",
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

		project, err := ag.DB.CreateProject(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error creating project:", err)
			os.Exit(1)
		}
		fmt.Printf("Project %q created.\n", name)

		model := tui.NewModel(ag, project, logger)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	},
}
