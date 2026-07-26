package llm

import (
	"strings"
	"testing"
)

func TestNewClientPresets(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantBase  string
		wantModel string
		wantStyle Style
	}{
		{
			name:      "gemini preset",
			env:       map[string]string{"WATERWRITER_LLM_PROVIDER": "gemini", "WATERWRITER_LLM_API_KEY": "x"},
			wantBase:  "https://generativelanguage.googleapis.com",
			wantModel: "gemini-3.5-flash",
			wantStyle: StyleGemini,
		},
		{
			name:      "anthropic preset",
			env:       map[string]string{"WATERWRITER_LLM_PROVIDER": "anthropic", "WATERWRITER_LLM_API_KEY": "x"},
			wantBase:  "https://api.anthropic.com",
			wantModel: "claude-sonnet-5",
			wantStyle: StyleAnthropic,
		},
		{
			name:      "openai override",
			env:       map[string]string{"WATERWRITER_LLM_PROVIDER": "openai", "WATERWRITER_LLM_API_KEY": "x", "WATERWRITER_LLM_MODEL": "gpt-4o", "WATERWRITER_LLM_BASE_URL": "https://example.com/v1"},
			wantBase:  "https://example.com/v1",
			wantModel: "gpt-4o",
			wantStyle: StyleOpenAI,
		},
		{
			name:      "custom style anthropic",
			env:       map[string]string{"WATERWRITER_LLM_PROVIDER": "custom", "WATERWRITER_LLM_BASE_URL": "https://x/v1", "WATERWRITER_LLM_MODEL": "m", "WATERWRITER_LLM_API_KEY": "k", "WATERWRITER_LLM_API_STYLE": "anthropic"},
			wantBase:  "https://x/v1",
			wantModel: "m",
			wantStyle: StyleAnthropic,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := NewClient()
			if c.BaseURL != tc.wantBase {
				t.Errorf("base URL = %q, want %q", c.BaseURL, tc.wantBase)
			}
			if c.Model != tc.wantModel {
				t.Errorf("model = %q, want %q", c.Model, tc.wantModel)
			}
			if c.Style != tc.wantStyle {
				t.Errorf("style = %q, want %q", c.Style, tc.wantStyle)
			}
		})
	}
}

func TestNewClientFromConfig(t *testing.T) {
	c := NewClientFromConfig(Config{
		Provider: "anthropic",
		APIKey:   "x",
		Model:    "claude-sonnet-5",
	})
	if c.Style != StyleAnthropic {
		t.Fatalf("style = %q, want anthropic", c.Style)
	}
	if c.Model != "claude-sonnet-5" {
		t.Fatalf("model = %q", c.Model)
	}
	// DB-backed custom provider with explicit style.
	c2 := NewClientFromConfig(Config{
		Provider: "custom",
		BaseURL:  "https://example.com/v1",
		Model:    "m",
		APIKey:   "k",
		Style:    "gemini",
	})
	if c2.Style != StyleGemini || c2.BaseURL != "https://example.com/v1" {
		t.Fatalf("c2 = %+v", c2)
	}
}

func TestReady(t *testing.T) {
	t.Run("ollama no key is ready", func(t *testing.T) {
		t.Setenv("WATERWRITER_LLM_PROVIDER", "ollama")
		c := NewClient()
		if ok, _ := c.Ready(); !ok {
			t.Fatalf("ollama without key should be Ready")
		}
	})
	t.Run("openai no key not ready", func(t *testing.T) {
		t.Setenv("WATERWRITER_LLM_PROVIDER", "openai")
		t.Setenv("WATERWRITER_LLM_API_KEY", "")
		c := NewClient()
		if ok, _ := c.Ready(); ok {
			t.Fatalf("openai without key should not be Ready")
		}
	})
}

func TestParseHeaders(t *testing.T) {
	h := parseHeaders("X-Custom: abc, Authorization: Bearer xyz, Org: my-org")
	if h["X-Custom"] != "abc" {
		t.Fatalf("X-Custom = %q", h["X-Custom"])
	}
	if h["Authorization"] != "Bearer xyz" {
		t.Fatalf("Authorization = %q (colon in value should be preserved)", h["Authorization"])
	}
	if h["Org"] != "my-org" {
		t.Fatalf("Org = %q", h["Org"])
	}
}

func TestBuildGeminiRequest(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "bye"},
	}
	req := buildGeminiRequest(msgs, 0.5)
	if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != "be concise" {
		t.Fatalf("systemInstruction not built: %+v", req.SystemInstruction)
	}
	if len(req.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(req.Contents))
	}
	if req.Contents[1].Role != "model" {
		t.Fatalf("assistant should map to model, got %q", req.Contents[1].Role)
	}
	if req.GenerationConfig.Temperature != 0.5 || req.GenerationConfig.MaxOutputTokens != 8192 {
		t.Fatalf("gen config wrong: %+v", req.GenerationConfig)
	}
}

func TestSplitSystem(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys1"},
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "sys2"},
	}
	system, rest := splitSystem(msgs)
	if system != "sys1\n\nsys2" {
		t.Fatalf("system = %q", system)
	}
	if len(rest) != 1 || rest[0].Role != "user" {
		t.Fatalf("rest = %+v", rest)
	}
}

// TestACPComplete tests the ACP subprocess protocol end-to-end.
// It requires the `opencode` binary to be installed and authenticated.
// Run with: go test -run TestACPComplete -v ./internal/llm/
func TestACPComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ACP integration test in short mode")
	}

	client := &Client{
		Provider:    "opencode-acp",
		Style:       StyleACP,
		Model:       "opencode-acp",
		RequiresKey: false,
	}

	// Check if the client is ready (binary exists in PATH).
	ready, msg := client.Ready()
	if !ready {
		t.Skipf("ACP not ready: %s. Run 'opencode auth login' first.", msg)
	}

	// Test generating a book title.
	ctx := t.Context()
	t.Log("Testing ACP Complete with a book title prompt...")
	result, err := client.Complete(ctx, []Message{
		{Role: "system", Content: "You are a book title generator. Be creative and concise."},
		{Role: "user", Content: "Generate a book title and subtitle about artificial intelligence and human consciousness. Format: Title: ... Subtitle: ..."},
	}, 0.7)
	if err != nil {
		t.Fatalf("ACP Complete failed: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("ACP returned empty response")
	}
	t.Logf("ACP response (%d chars):\n%s", len(result), result)

	// Verify the response looks like book content.
	if !strings.Contains(result, ":") && !strings.Contains(result, "Title") {
		t.Logf("WARNING: response may not be a proper book title (no colon or 'Title' found)")
	}

	t.Log("--- ACP Complete test PASSED ---")
}

// TestACPStream tests streaming via the ACP subprocess protocol.
// It requires the `opencode` binary to be installed and authenticated.
// Run with: go test -run TestACPStream -v ./internal/llm/
func TestACPStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ACP integration test in short mode")
	}

	client := &Client{
		Provider:    "opencode-acp",
		Style:       StyleACP,
		Model:       "opencode-acp",
		RequiresKey: false,
	}

	ready, msg := client.Ready()
	if !ready {
		t.Skipf("ACP not ready: %s. Run 'opencode auth login' first.", msg)
	}

	ctx := t.Context()
	var chunks []string
	t.Log("Testing ACP streaming...")

	result, err := client.CompleteStream(ctx, []Message{
		{Role: "system", Content: "You are a helpful writing assistant. Be concise."},
		{Role: "user", Content: "Write a single sentence about the future of AI."},
	}, 0.7, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})

	if err != nil {
		t.Fatalf("ACP Stream failed: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("ACP streaming returned empty result")
	}
	t.Logf("ACP streaming result (%d chars):\n%s", len(result), result)
	t.Logf("Received %d streaming chunks", len(chunks))

	t.Log("--- ACP Stream test PASSED ---")
}

// TestACPListModels tests the ACP model listing.
func TestACPListModels(t *testing.T) {
	client := &Client{
		Provider:    "opencode-acp",
		Style:       StyleACP,
		Model:       "opencode-acp",
		RequiresKey: false,
	}

	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ACP ListModels failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ACP ListModels returned empty list")
	}
	t.Logf("ACP models: %v", models)
}

// TestACPReady tests the ACP Ready check.
func TestACPReady(t *testing.T) {
	client := &Client{
		Provider:    "opencode-acp",
		Style:       StyleACP,
		Model:       "opencode-acp",
		RequiresKey: false,
	}

	ready, msg := client.Ready()
	t.Logf("ACP Ready: %v, msg: %s", ready, msg)

	if !ready {
		t.Logf("ACP not ready. This is expected if opencode is not in PATH or not authenticated.")
		t.Logf("To fix: run 'opencode auth login' and ensure opencode is in PATH")
	}
}
