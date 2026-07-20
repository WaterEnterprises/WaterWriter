package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export <project-name> [output-dir]",
	Short: "Export the completed book to markdown files",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		outDir := "."
		if len(args) > 1 {
			outDir = args[1]
		}

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

		book, err := ag.DB.GetBook(project.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Book not found for project %q. Write the book first.\n", name)
			os.Exit(1)
		}

		chapters, err := ag.DB.GetChapters(project.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		dir := filepath.Join(outDir, sanitize(book.Title))
		os.MkdirAll(dir, 0o755)

		var fullBook strings.Builder
		fullBook.WriteString(fmt.Sprintf("# %s\n\n", book.Title))
		if book.Subtitle != "" {
			fullBook.WriteString(fmt.Sprintf("## %s\n\n", book.Subtitle))
		}

		for _, ch := range chapters {
			fullBook.WriteString(fmt.Sprintf("\n## %s\n\n", ch.Title))
			subs, _ := ag.DB.GetSubchapters(ch.ID)

			for _, s := range subs {
				if s.Content != "" {
					fullBook.WriteString(fmt.Sprintf("### %s\n\n", s.Title))
					fullBook.WriteString(s.Content)
					fullBook.WriteString("\n\n")
				}
			}
		}

		bookPath := filepath.Join(dir, "book.md")
		os.WriteFile(bookPath, []byte(fullBook.String()), 0o644)
		fmt.Printf("Book exported to %s\n", bookPath)
	},
}

func sanitize(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return r.Replace(s)
}
