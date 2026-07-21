package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZeroHawkeye/wordZero/pkg/document"
	"github.com/spf13/cobra"
)

var (
	exportIncludeSubs bool
	exportFormat      string
)

func init() {
	exportCmd.Flags().BoolVarP(&exportIncludeSubs, "subs", "s", true, "Include subchapters in the table of contents")
	exportCmd.Flags().StringVarP(&exportFormat, "format", "f", "markdown", "Export format: markdown or docx")
}

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

		// Generate Table of Contents between title and body
		fullBook.WriteString("## Table of Contents\n\n")
		for i, ch := range chapters {
			fullBook.WriteString(fmt.Sprintf("- [Chapter %d: %s](#chapter-%d-%s)\n",
				i+1, ch.Title, i+1, tocAnchor(ch.Title)))
			if exportIncludeSubs {
				subs, _ := ag.DB.GetSubchapters(ch.ID)
				for j, s := range subs {
					if s.Content != "" {
						fullBook.WriteString(fmt.Sprintf("  - [%d.%d %s](#%s)\n",
							i+1, j+1, s.Title, tocAnchor(s.Title)))
					}
				}
			}
		}
		fullBook.WriteString("\n---\n\n")

		for i, ch := range chapters {
			fullBook.WriteString(fmt.Sprintf("\n## Chapter %d: %s\n\n", i+1, ch.Title))
			subs, _ := ag.DB.GetSubchapters(ch.ID)

			for _, s := range subs {
				if s.Content != "" {
					// Strip leading #-style markdown headings from LLM content.
					content := strings.TrimLeft(s.Content, "\n\r\t ")
					for strings.HasPrefix(content, "#") {
						idx := strings.IndexByte(content, '\n')
						if idx < 0 {
							content = ""
							break
						}
						content = strings.TrimLeft(content[idx+1:], "\n\r\t ")
					}
					if content == "" {
						continue
					}
					fullBook.WriteString(fmt.Sprintf("### %s\n\n", s.Title))
					fullBook.WriteString(content)
					fullBook.WriteString("\n\n")
				}
			}
		}

		bookPath := filepath.Join(dir, "book.md")
		os.WriteFile(bookPath, []byte(fullBook.String()), 0o644)
		fmt.Printf("Book exported to %s\n", bookPath)

		if exportFormat == "docx" {
			doc := document.New()
			doc.AddHeadingParagraph(book.Title, 1)
			if book.Subtitle != "" {
				doc.AddHeadingParagraph(book.Subtitle, 2)
			}

			doc.AddHeadingParagraph("Table of Contents", 2)
			for i, ch := range chapters {
				doc.AddParagraph(fmt.Sprintf("Chapter %d: %s", i+1, ch.Title))
				if exportIncludeSubs {
					subs, _ := ag.DB.GetSubchapters(ch.ID)
					for _, s := range subs {
						doc.AddParagraph(fmt.Sprintf("  %d.%d %s", i+1, s.Position, s.Title))
					}
				}
			}

			for i, ch := range chapters {
				doc.AddHeadingParagraph(fmt.Sprintf("Chapter %d: %s", i+1, ch.Title), 2)
				if exportIncludeSubs {
					subs, _ := ag.DB.GetSubchapters(ch.ID)
					for _, s := range subs {
						if s.Content == "" {
							continue
						}
						content := strings.TrimLeft(s.Content, "\n\r\t ")
						for strings.HasPrefix(content, "#") {
							idx := strings.IndexByte(content, '\n')
							if idx < 0 {
								content = ""
								break
							}
							content = strings.TrimLeft(content[idx+1:], "\n\r\t ")
						}
						if content == "" {
							continue
						}
						doc.AddHeadingParagraph(s.Title, 3)
						for _, p := range strings.Split(content, "\n\n") {
							p = strings.TrimSpace(p)
							if p != "" {
								// Collapse single newlines to spaces (markdown behavior).
								doc.AddParagraph(strings.Join(strings.Fields(p), " "))
							}
						}
					}
				}
			}

			docxPath := filepath.Join(dir, "book.docx")
			if err := doc.Save(docxPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving docx: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Book exported to %s\n", docxPath)
		}
	},
}

func sanitize(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return r.Replace(s)
}

func tocAnchor(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == ' ' {
			if r == ' ' {
				b.WriteByte('-')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
