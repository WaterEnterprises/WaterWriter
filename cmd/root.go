package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/WaterEnterprises/WaterWriter/internal/agent"
	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/WaterEnterprises/WaterWriter/internal/llm"
	"github.com/WaterEnterprises/WaterWriter/internal/log"
	"github.com/WaterEnterprises/WaterWriter/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/inconshreveable/mousetrap"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "waterwriter",
	Short: "Water Writer — AI-powered book writing agent",
	Long: `Water Writer is an AI agent that helps you write books.
It interviews you about your book, generates a title and table of contents,
then writes each subchapter in a fully autonomous workflow.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// Launched with no subcommand. If this is an interactive terminal,
		// open the TUI home screen so the app can be used by double-clicking.
		// Otherwise print help so piping / non-interactive use still behaves
		// like a normal command-line tool.
		if !isTerminal(os.Stdout) {
			_ = cmd.Help()
			return
		}
		// When started by double-clicking (from explorer.exe) on Windows, the
		// inherited console is not interactive, so the TUI can't read input and
		// appears frozen. Relaunch in a fresh, interactive console window.
		if runtime.GOOS == "windows" && os.Getenv("WATERWRITER_REEXEC") == "" && mousetrap.StartedByExplorer() {
			if exe, err := os.Executable(); err == nil {
				params := append([]string{"/c", "start", "", exe}, os.Args[1:]...)
				c := exec.Command("cmd.exe", params...)
				c.Env = append(os.Environ(), "WATERWRITER_REEXEC=1")
				_ = c.Start()
				os.Exit(0)
			}
		}
		logger, llmClient, ag, err := initApp()
		if logger != nil {
			defer logger.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		llmReady := true
		llmWarning := ""
		if ok, msg := llmClient.Ready(); !ok {
			llmReady = false
			llmWarning = msg
		}
		model := tui.NewHomeModel(ag, logger, llmReady, llmWarning)
		p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	},
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

var dbPath string

func Execute() {
	// Disable Cobra's Windows "mousetrap": when launched by double-clicking the
	// exe (from explorer.exe) Cobra would otherwise print "This is a command
	// line tool..." and exit. We want the TUI to open instead.
	cobra.MousetrapHelpText = ""

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", defaultDBPath(), "path to SQLite database")
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(openCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(providersCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(configCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./waterwriter.db"
	}
	dir := filepath.Join(home, ".waterwriter")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "waterwriter.db")
}

func initApp() (*log.Logger, *llm.Client, *agent.Agent, error) {
	godotenv.Load()

	// Initialize the file logger. Fail gracefully on error (logging is optional).
	logger, err := log.New(log.DefaultLogPath())
	if err != nil {
		// Logging is best-effort; continue without it.
		fmt.Fprintf(os.Stderr, "Warning: could not create logger: %v\n", err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		if logger != nil {
			logger.Close()
		}
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}

	// Merge saved database settings with environment variables. The database
	// holds the user's selected provider/model/API key; env can override any
	// saved value (useful for CI or power users).
	saved, err := database.GetSettings(
		db.SettingProvider, db.SettingModel, db.SettingBaseURL, db.SettingStyle,
		db.SettingAPIKey, db.SettingThinkingEffort,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load settings: %w", err)
	}
	cfg := llm.Config{
		Provider:       firstNonEmpty(saved[db.SettingProvider], os.Getenv("WATERWRITER_LLM_PROVIDER")),
		BaseURL:        firstNonEmpty(saved[db.SettingBaseURL], os.Getenv("WATERWRITER_LLM_BASE_URL")),
		Model:          firstNonEmpty(saved[db.SettingModel], os.Getenv("WATERWRITER_LLM_MODEL")),
		Style:          firstNonEmpty(saved[db.SettingStyle], os.Getenv("WATERWRITER_LLM_API_STYLE")),
		APIKey:         firstNonEmpty(saved[db.SettingAPIKey], os.Getenv("WATERWRITER_LLM_API_KEY")),
		ThinkingEffort: saved[db.SettingThinkingEffort],
		ExtraHeaders:   os.Getenv("WATERWRITER_LLM_EXTRA_HEADERS"),
	}
	llmClient := llm.NewClientFromConfig(cfg)
	ag := agent.New(llmClient, database, logger)
	if logger != nil {
		logger.Info("Water Writer started: provider=%s model=%s", cfg.Provider, cfg.Model)
		logger.Info("Database: %s", dbPath)
	}
	return logger, llmClient, ag, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func requireLLM(llmClient *llm.Client) error {
	if ok, msg := llmClient.Ready(); !ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}
