package cmd

import (
	"fmt"

	"github.com/WaterEnterprises/WaterWriter/internal/llm"
	"github.com/spf13/cobra"
)

var providersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List supported LLM providers",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Supported LLM providers (set WATERWRITER_LLM_PROVIDER):")
		for _, key := range llm.ListProviders() {
			p := llm.Providers[key]
			fmt.Printf("  %-12s %s\n", key, p.Name)
			if p.BaseURL != "" {
				fmt.Printf("               default base URL: %s\n", p.BaseURL)
			}
			if p.DefaultModel != "" {
				fmt.Printf("               default model:   %s\n", p.DefaultModel)
			}
			if !p.RequiresKey {
				fmt.Printf("               API key: not required\n")
			}
		}
		fmt.Println("\nAny OpenAI-compatible endpoint can be used with: --provider custom")
		fmt.Println("and WATERWRITER_LLM_BASE_URL / WATERWRITER_LLM_MODEL set.")
	},
}
