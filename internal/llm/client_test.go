package llm

import (
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
