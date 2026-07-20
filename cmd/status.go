package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <project-name>",
	Short: "Show detailed status of a book project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		_, _, ag, err := initApp()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		project, err := ag.DB.GetProject(name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintf(os.Stderr, "Project %q not found.\n", name)
			} else {
				fmt.Fprintln(os.Stderr, "Error:", err)
			}
			os.Exit(1)
		}
		phase := ag.DB.GetPhase(project.ID)

		fmt.Printf("Project: %s\n", project.Name)
		fmt.Printf("Status: %s\n", project.Status)
		fmt.Printf("Phase: %s\n\n", phase)

		qa, _ := ag.DB.GetQAPairs(project.ID)
		fmt.Printf("Q&A Pairs: %d\n", len(qa))

		brief, _ := ag.DB.GetBrief(project.ID)
		if brief != "" {
			fmt.Printf("Brief: %d chars\n\n", len(brief))
		}

		book, err := ag.DB.GetBook(project.ID)
		if err == nil {
			fmt.Printf("Title: %s\n", book.Title)
			if book.Subtitle != "" {
				fmt.Printf("Subtitle: %s\n", book.Subtitle)
			}
			fmt.Println()

			chapters, _ := ag.DB.GetChapters(project.ID)
			for i, ch := range chapters {
				subs, _ := ag.DB.GetSubchapters(ch.ID)
				done := 0
				for _, s := range subs {
					if s.Status == "done" {
						done++
					}
				}
				mark := " "
				if ch.Status == "done" {
					mark = "✓"
				}
				fmt.Printf("  %s Chapter %d: %s (%d/%d subchapters)\n", mark, i+1, ch.Title, done, len(subs))
			}
		}
	},
}
