package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all book projects",
	Run: func(cmd *cobra.Command, args []string) {
		_, _, ag, err := initApp()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		projects, err := ag.DB.ListProjects()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return
		}
		for _, p := range projects {
			phase := ag.DB.GetPhase(p.ID)
			book, _ := ag.DB.GetBook(p.ID)
			title := ""
			if book != nil {
				title = book.Title
			}
			fmt.Printf("  %-20s  phase: %-12s  title: %s\n", p.Name, phase, title)
		}
	},
}
