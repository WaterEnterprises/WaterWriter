package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var translateCmd = &cobra.Command{
	Use:   "translate <project-name> <language> [output-dir]",
	Short: "Translate a book to another language",
	Long: `Translates an entire book (title, chapters, and all subchapter content)
into the specified language using the LLM. The translation is done
subchapter-by-subchapter to ensure accuracy and avoid truncation.

Example:
  waterwriter translate "My Book" Portuguese
  waterwriter translate "My Book" French ./translations
  waterwriter translate "Radiohead SOS" "Brazilian Portuguese"`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		language := args[1]
		outDir := "."
		if len(args) > 2 {
			outDir = args[2]
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
			fmt.Fprintf(os.Stderr, "Book not found for project %q.\n", name)
			os.Exit(1)
		}

		chapters, err := ag.DB.GetChapters(project.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		fmt.Printf("Translating %q to %s...\n\n", name, language)

		// Step 1: Translate title, subtitle, and chapter titles
		fmt.Println("Step 1/3: Translating title and table of contents...")
		translated, err := ag.TranslateTitleTOC(context.Background(), language, book.Title, book.Subtitle, chapters)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error translating title/TOC: %v\n", err)
			os.Exit(1)
		}
		if len(translated) < 2 {
			fmt.Fprintln(os.Stderr, "Error: translation returned incomplete data")
			os.Exit(1)
		}
		transTitle := translated[0]
		transSubtitle := translated[1]
		transChapters := translated[2:]
		fmt.Printf("  Title: %s\n", transTitle)
		if transSubtitle != "" {
			fmt.Printf("  Subtitle: %s\n", transSubtitle)
		}
		fmt.Printf("  Chapters: %d\n\n", len(transChapters))

		// Step 2: Build the translated book markdown
		var fullBook strings.Builder
		fullBook.WriteString(fmt.Sprintf("# %s\n\n", transTitle))
		if transSubtitle != "" {
			fullBook.WriteString(fmt.Sprintf("## %s\n\n", transSubtitle))
		}
		fullBook.WriteString("---\n\n")

		// Step 3: Translate each subchapter
		for i, ch := range chapters {
			chTitle := transChapters[i]
			fmt.Printf("Step 2/3: Chapter %d/%d: %s\n", i+1, len(chapters), chTitle)
			fullBook.WriteString(fmt.Sprintf("## Chapter %d: %s\n\n", i+1, chTitle))

			subs, err := ag.DB.GetSubchapters(ch.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Error getting subchapters: %v\n", err)
				continue
			}

			for j, s := range subs {
				if s.Content == "" {
					continue
				}
				fmt.Printf("  Subchapter %d/%d: %s... ", j+1, len(subs), ellipsize(s.Title, 50))

				translatedContent, err := ag.TranslateSubchapterContent(context.Background(),
					language,
					book.Title, book.Subtitle,
					ch.Title, s.Title, s.Content)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
					// Use original content as fallback
					translatedContent = s.Content
				} else {
					fmt.Println("OK")
				}

				fullBook.WriteString(fmt.Sprintf("### %s\n\n", s.Title))
				fullBook.WriteString(translatedContent)
				fullBook.WriteString("\n\n")
			}
		}

		// Write the translated book to file
		dir := filepath.Join(outDir, sanitize(transTitle)+"_"+sanitize(language))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
			os.Exit(1)
		}
		bookPath := filepath.Join(dir, "book.md")
		if err := os.WriteFile(bookPath, []byte(fullBook.String()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n✓ Translated book saved to %s\n", bookPath)
	},
}

func ellipsize(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func init() {
	rootCmd.AddCommand(translateCmd)
}
