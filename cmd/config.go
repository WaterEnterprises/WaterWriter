package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/WaterEnterprises/WaterWriter/internal/llm"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	cfgProvider string
	cfgModel    string
	cfgBaseURL  string
	cfgStyle    string
	cfgSelect   bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View or change the saved LLM provider and model",
	Long: `View or change the LLM provider/model saved in the database.

With no flags, prints the currently saved configuration (falling back to
environment variables / provider presets). Examples:

  waterwriter config                           # show current config
  waterwriter config --provider gemini         # switch provider, then pick a model
  waterwriter config --select                  # pick a model from the live endpoint
  waterwriter config --provider openai --model gpt-5.6-sol

When you change the provider (or pass --select) without giving --model, the
tool queries the endpoint for its available models and lets you choose one
interactively, so you always pick a real, current model instead of a hardcoded
default.

The API key is never stored in the database; it is always read from the
WATERWRITER_LLM_API_KEY environment variable (or .env).`,
	Run: func(cmd *cobra.Command, args []string) {
		godotenv.Load()
		database, err := db.Open(dbPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		defer database.Close()

		saved, err := database.GetSettings(
			db.SettingProvider, db.SettingModel, db.SettingBaseURL, db.SettingStyle,
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		changing := cfgProvider != "" || cfgModel != "" || cfgBaseURL != "" || cfgStyle != ""

		// Save the non-model overrides first so endpoint resolution is correct.
		overrideValues := map[string]string{}
		if cfgProvider != "" {
			overrideValues[db.SettingProvider] = cfgProvider
		}
		if cfgBaseURL != "" {
			overrideValues[db.SettingBaseURL] = cfgBaseURL
		}
		if cfgStyle != "" {
			overrideValues[db.SettingStyle] = cfgStyle
		}
		if len(overrideValues) > 0 {
			if err := database.SetSettings(overrideValues); err != nil {
				fmt.Fprintln(os.Stderr, "Error saving config:", err)
				os.Exit(1)
			}
		}

		// Decide whether to offer an interactive model pick from the endpoint.
		wantPick := cfgModel == "" && (cfgSelect || cfgProvider != "" || cfgBaseURL != "" || cfgStyle != "")
		if wantPick {
			targetProvider := firstNonEmpty(cfgProvider, saved[db.SettingProvider], os.Getenv("WATERWRITER_LLM_PROVIDER"))
			targetBaseURL := firstNonEmpty(cfgBaseURL, saved[db.SettingBaseURL], os.Getenv("WATERWRITER_LLM_BASE_URL"))
			targetStyle := firstNonEmpty(cfgStyle, saved[db.SettingStyle], os.Getenv("WATERWRITER_LLM_API_STYLE"))
			model, perr := pickModelFromEndpoint(targetProvider, targetBaseURL, targetStyle, os.Getenv("WATERWRITER_LLM_API_KEY"))
			if perr != nil {
				fmt.Fprintln(os.Stderr, "Model selection skipped:", perr)
			} else {
				cfgModel = model
				if err := database.SetSetting(db.SettingModel, model); err != nil {
					fmt.Fprintln(os.Stderr, "Error saving model:", err)
					os.Exit(1)
				}
			}
		}

		if changing || wantPick {
			fmt.Println("Saved configuration:")
		} else {
			fmt.Println("Current configuration (database + environment):")
		}

		// Re-read so the printed values reflect everything just saved.
		saved, err = database.GetSettings(
			db.SettingProvider, db.SettingModel, db.SettingBaseURL, db.SettingStyle,
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		// Resolve to what would actually be used (mirrors initApp: saved
		// database settings take precedence over env/preset defaults).
		resolved := llm.NewClientFromConfig(llm.Config{
			Provider: saved[db.SettingProvider],
			Model:    saved[db.SettingModel],
			BaseURL:  saved[db.SettingBaseURL],
			Style:    saved[db.SettingStyle],
			APIKey:   os.Getenv("WATERWRITER_LLM_API_KEY"),
		})
		rows := []struct{ key, value, note string }{
			{"provider", firstNonEmpty(saved[db.SettingProvider], resolved.Provider), source(saved[db.SettingProvider], "env/preset")},
			{"model", firstNonEmpty(saved[db.SettingModel], resolved.Model), source(saved[db.SettingModel], "env/preset")},
			{"base_url", firstNonEmpty(saved[db.SettingBaseURL], resolved.BaseURL), source(saved[db.SettingBaseURL], "env/preset")},
			{"style", firstNonEmpty(saved[db.SettingStyle], string(resolved.Style)), source(saved[db.SettingStyle], "env/preset")},
		}
		for _, r := range rows {
			fmt.Printf("  %-10s %-42s (%s)\n", r.key, r.value, r.note)
		}
		fmt.Printf("\nAPI key: from environment (WATERWRITER_LLM_API_KEY), not stored.\n")
	},
}

// pickModelFromEndpoint queries the provider for its available models and lets
// the user choose one interactively. A blank line or invalid number aborts the
// selection; typing an exact model ID is also accepted.
func pickModelFromEndpoint(provider, baseURL, style, apiKey string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("WATERWRITER_LLM_API_KEY is not set; cannot query the endpoint")
	}
	c := llm.NewClientFromConfig(llm.Config{
		Provider: provider,
		BaseURL:  baseURL,
		Style:    style,
		APIKey:   apiKey,
	})
	models, err := c.ListModels(context.Background())
	if err != nil {
		return "", fmt.Errorf("could not list models: %w", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("endpoint returned no models")
	}

	const maxShow = 60
	shown := models
	if len(shown) > maxShow {
		shown = shown[:maxShow]
	}
	fmt.Printf("\nAvailable models on %s (showing %d of %d):\n", provider, len(shown), len(models))
	for i, m := range shown {
		fmt.Printf("  %3d) %s\n", i+1, m)
	}
	if len(models) > maxShow {
		fmt.Printf("  ... (%d more not shown)\n", len(models)-maxShow)
	}
	fmt.Print("Select a model number, or type an exact model ID: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("no selection made")
	}
	if n, err := strconv.Atoi(line); err == nil {
		if n < 1 || n > len(models) {
			return "", fmt.Errorf("selection %d is out of range (1-%d)", n, len(models))
		}
		return models[n-1], nil
	}
	return line, nil
}

func source(saved, fallback string) string {
	if strings.TrimSpace(saved) != "" {
		return "saved in database"
	}
	return fallback
}

func init() {
	configCmd.Flags().StringVar(&cfgProvider, "provider", "", "LLM provider key (e.g. openai, anthropic, gemini)")
	configCmd.Flags().StringVar(&cfgModel, "model", "", "model ID to use")
	configCmd.Flags().StringVar(&cfgBaseURL, "base-url", "", "override base URL (custom endpoints)")
	configCmd.Flags().StringVar(&cfgStyle, "style", "", "API style: openai | anthropic | gemini")
	configCmd.Flags().BoolVar(&cfgSelect, "select", false, "interactively pick a model from the endpoint for the current provider")
}
