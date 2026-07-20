package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List models available from the configured provider",
	Long: `Query the configured LLM provider for the models it exposes.

Set WATERWRITER_LLM_PROVIDER / WATERWRITER_LLM_API_KEY (and optionally
WATERWRITER_LLM_BASE_URL) first. For example:

  WATERWRITER_LLM_PROVIDER=openai waterwriter models
  WATERWRITER_LLM_PROVIDER=anthropic waterwriter models
  WATERWRITER_LLM_PROVIDER=gemini waterwriter models`,
	Run: func(cmd *cobra.Command, args []string) {
		_, llmClient, _, err := initApp()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		if ok, msg := llmClient.Ready(); !ok {
			fmt.Fprintln(os.Stderr, "Error:", msg)
			os.Exit(1)
		}
		models, err := llmClient.ListModels(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error listing models:", err)
			os.Exit(1)
		}
		if len(models) == 0 {
			fmt.Printf("No models returned by provider %q.\n", llmClient.Provider)
			return
		}
		fmt.Printf("Models available from provider %q:\n", llmClient.Provider)
		for _, m := range models {
			fmt.Printf("  %s\n", m)
		}
		fmt.Printf("\nSet one with: export WATERWRITER_LLM_MODEL=<model-id>\n")
	},
}
