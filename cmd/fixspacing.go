package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/spf13/cobra"
)

var fixSpacingDryRun bool

func init() {
	fixSpacingCmd.Flags().BoolVar(&fixSpacingDryRun, "dry-run", false, "Preview changes without modifying the database")
	rootCmd.AddCommand(fixSpacingCmd)
}

var fixSpacingCmd = &cobra.Command{
	Use:   "fix-spacing [project-name]",
	Short: "Fix merged-word spacing in existing subchapter content",
	Long: `Re-runs the spacing normalization pipeline (regex + dictionary-based word
segmentation) on all existing subchapter content in the database. This
fixes merged words like "blanketof" → "blanket of" that were generated
by LLM tokenizer quirks.

If a project name is given, only that project is processed.
Otherwise, all projects are processed.

Use --dry-run to preview changes without writing to the database.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, _, ag, err := initApp()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		// Determine which projects to process.
		var projects []*db.Project
		if len(args) == 1 {
			p, err := ag.DB.GetProject(args[0])
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					fmt.Fprintf(os.Stderr, "Project %q not found.\n", args[0])
				} else {
					fmt.Fprintln(os.Stderr, "Error:", err)
				}
				os.Exit(1)
			}
			projects = append(projects, p)
		} else {
			ps, err := ag.DB.ListProjects()
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error listing projects:", err)
				os.Exit(1)
			}
			projects = ps
		}

		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return
		}

		totalChecked := 0
		totalFixed := 0

		for _, proj := range projects {
			chapters, err := ag.DB.GetChapters(proj.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Error getting chapters for %q: %v\n", proj.Name, err)
				continue
			}

			projectFixed := 0
			for _, ch := range chapters {
				subs, err := ag.DB.GetSubchapters(ch.ID)
				if err != nil {
					continue
				}
				for _, s := range subs {
					if s.Content == "" {
						continue
					}
					totalChecked++
					cleaned := normalizeSpacing(s.Content)
					if cleaned != s.Content {
						if fixSpacingDryRun {
							fmt.Printf("  Would fix subchapter %d in %s / %s\n", s.ID, proj.Name, ch.Title)
						} else {
							if err := ag.DB.UpdateSubchapterContent(s.ID, cleaned); err != nil {
								fmt.Fprintf(os.Stderr, "  Error updating subchapter %d: %v\n", s.ID, err)
								continue
							}
						}
						projectFixed++
						totalFixed++
					}
				}
			}

			if fixSpacingDryRun {
				fmt.Printf("  %s: %d subchapters would be fixed\n", proj.Name, projectFixed)
			} else if projectFixed > 0 {
				fmt.Printf("  %s: fixed %d subchapters\n", proj.Name, projectFixed)
			} else {
				fmt.Printf("  %s: no fixes needed\n", proj.Name)
			}
		}

		if fixSpacingDryRun {
			fmt.Printf("\nDry run complete. Would fix %d of %d subchapters checked.\n", totalFixed, totalChecked)
		} else {
			fmt.Printf("\nDone! Checked %d subchapters, fixed %d.\n", totalChecked, totalFixed)
		}
	},
}
