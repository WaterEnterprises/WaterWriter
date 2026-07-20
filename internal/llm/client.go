package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Style string

const (
	StyleOpenAI    Style = "openai"
	StyleAnthropic Style = "anthropic"
	StyleGemini    Style = "gemini"
)

// ProviderPreset describes a known LLM provider and how to talk to it.
type ProviderPreset struct {
	Name         string
	BaseURL      string
	DefaultModel string
	Style        Style
	RequiresKey  bool
}

// Providers is the catalogue of built-in, pre-configured LLM providers.
// Anything OpenAI-compatible can also be used via the "custom" provider by
// setting WATERWRITER_LLM_BASE_URL / WATERWRITER_LLM_MODEL.
var Providers = map[string]ProviderPreset{
	"openai": {
		Name:         "OpenAI",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5.6-sol",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"anthropic": {
		Name:         "Anthropic Claude",
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-5",
		Style:        StyleAnthropic,
		RequiresKey:  true,
	},
	"gemini": {
		Name:         "Google Gemini (AI Studio, native)",
		BaseURL:      "https://generativelanguage.googleapis.com",
		DefaultModel: "gemini-3.5-flash",
		Style:        StyleGemini,
		RequiresKey:  true,
	},
	"deepseek": {
		Name:         "DeepSeek",
		BaseURL:      "https://api.deepseek.com",
		DefaultModel: "deepseek-chat",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"groq": {
		Name:         "Groq",
		BaseURL:      "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"openrouter": {
		Name:         "OpenRouter",
		BaseURL:      "https://openrouter.ai/api/v1",
		DefaultModel: "openai/gpt-4o-mini",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"together": {
		Name:         "Together AI",
		BaseURL:      "https://api.together.xyz/v1",
		DefaultModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"ollama": {
		Name:         "Ollama (local)",
		BaseURL:      "http://localhost:11434/v1",
		DefaultModel: "llama3",
		Style:        StyleOpenAI,
		RequiresKey:  false,
	},
	"xai": {
		Name:         "xAI Grok",
		BaseURL:      "https://api.x.ai/v1",
		DefaultModel: "grok-2-latest",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"mistral": {
		Name:         "Mistral AI",
		BaseURL:      "https://api.mistral.ai/v1",
		DefaultModel: "mistral-large-latest",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"perplexity": {
		Name:         "Perplexity",
		BaseURL:      "https://api.perplexity.ai",
		DefaultModel: "sonar",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"fireworks": {
		Name:         "Fireworks AI",
		BaseURL:      "https://api.fireworks.ai/inference/v1",
		DefaultModel: "accounts/fireworks/models/llama-v3p3-70b-instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"huggingface": {
		Name:         "Hugging Face Inference",
		BaseURL:      "https://api-inference.huggingface.co/v1",
		DefaultModel: "meta-llama/Llama-3.3-70B-Instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"deepinfra": {
		Name:         "DeepInfra",
		BaseURL:      "https://api.deepinfra.com/v1/openai",
		DefaultModel: "meta-llama/Llama-3.3-70B-Instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"novita": {
		Name:         "Novita AI",
		BaseURL:      "https://api.novita.ai/v3/openai",
		DefaultModel: "meta-llama/llama-3.3-70b-instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"nvidia": {
		Name:         "NVIDIA NIM",
		BaseURL:      "https://integrate.api.nvidia.com/v1",
		DefaultModel: "nvidia/llama-3.1-nemotron-70b-instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"siliconflow": {
		Name:         "SiliconFlow",
		BaseURL:      "https://api.siliconflow.cn/v1",
		DefaultModel: "deepseek-ai/DeepSeek-V3",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"qwen": {
		Name:         "Alibaba Qwen (DashScope)",
		BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel: "qwen-max",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"moonshot": {
		Name:         "Moonshot AI (Kimi)",
		BaseURL:      "https://api.moonshot.cn/v1",
		DefaultModel: "moonshot-v1-8k",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"zhipu": {
		Name:         "Zhipu AI (GLM)",
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
		DefaultModel: "glm-4",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"replicate": {
		Name:         "Replicate (OpenAI-compatible)",
		BaseURL:      "https://api.replicate.com/v1",
		DefaultModel: "meta/meta-llama-3-70b-instruct",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"anyscale": {
		Name:         "Anyscale",
		BaseURL:      "https://api.endpoints.anyscale.com/v1",
		DefaultModel: "meta-llama/Llama-3-70b-chat-hf",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"scaleway": {
		Name:         "Scaleway Inference",
		BaseURL:      "https://api.scaleway.ai/v1",
		DefaultModel: "openai/o3-mini",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
	"vllm": {
		Name:         "vLLM (local OpenAI server)",
		BaseURL:      "http://localhost:8000/v1",
		DefaultModel: "",
		Style:        StyleOpenAI,
		RequiresKey:  false,
	},
	"lmstudio": {
		Name:         "LM Studio (local)",
		BaseURL:      "http://localhost:1234/v1",
		DefaultModel: "local-model",
		Style:        StyleOpenAI,
		RequiresKey:  false,
	},
	"textgenwebui": {
		Name:         "Text Generation WebUI (local)",
		BaseURL:      "http://localhost:5000/v1",
		DefaultModel: "textgen",
		Style:        StyleOpenAI,
		RequiresKey:  false,
	},
	"custom": {
		Name:         "Custom OpenAI-compatible",
		BaseURL:      "",
		DefaultModel: "",
		Style:        StyleOpenAI,
		RequiresKey:  true,
	},
}

// ListProviders returns the preset provider keys in a stable order.
func ListProviders() []string {
	out := make([]string, 0, len(Providers))
	for k := range Providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type Client struct {
	Provider     string
	BaseURL      string
	APIKey       string
	Model        string
	Style        Style
	RequiresKey  bool
	ExtraHeaders map[string]string
	HTTP         *http.Client
}

// Config is the resolved LLM configuration. Any field left empty falls back to
// the provider preset default (or the matching environment variable when
// NewClient is used). The API key is never stored in the database; it always
// comes from the environment.
type Config struct {
	Provider     string
	BaseURL      string
	APIKey       string
	Model        string
	Style        string // "openai" | "anthropic" | "gemini"
	ExtraHeaders string
}

// NewClient builds a client from environment variables.
//
// Env overrides (all optional except the key when the provider needs one):
//   - WATERWRITER_LLM_PROVIDER   provider key (default "openai")
//   - WATERWRITER_LLM_BASE_URL   override base URL (custom endpoints)
//   - WATERWRITER_LLM_MODEL      override model name
//   - WATERWRITER_LLM_API_KEY    provider API key
//   - WATERWRITER_LLM_API_STYLE  "openai" | "anthropic" | "gemini"
//   - WATERWRITER_LLM_EXTRA_HEADERS  "Name: Value, Other: Value2"
func NewClient() *Client {
	return NewClientFromConfig(Config{
		Provider:     os.Getenv("WATERWRITER_LLM_PROVIDER"),
		BaseURL:      os.Getenv("WATERWRITER_LLM_BASE_URL"),
		APIKey:       os.Getenv("WATERWRITER_LLM_API_KEY"),
		Model:        os.Getenv("WATERWRITER_LLM_MODEL"),
		Style:        os.Getenv("WATERWRITER_LLM_API_STYLE"),
		ExtraHeaders: os.Getenv("WATERWRITER_LLM_EXTRA_HEADERS"),
	})
}

// NewClientFromConfig builds a client from an explicit (e.g. database-backed)
// configuration. This is how saved provider/model selections are applied.
func NewClientFromConfig(cfg Config) *Client {
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = "openai"
	}

	preset, ok := Providers[provider]
	if !ok {
		// Unknown provider name -> treat as custom.
		preset = Providers["custom"]
		provider = "custom"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	model := cfg.Model
	if model == "" {
		model = preset.DefaultModel
	}
	styleStr := strings.ToLower(cfg.Style)
	style := preset.Style
	if styleStr == "anthropic" {
		style = StyleAnthropic
	} else if styleStr == "openai" {
		style = StyleOpenAI
	} else if styleStr == "gemini" {
		style = StyleGemini
	}
	apiKey := cfg.APIKey

	extraHeaders := parseHeaders(cfg.ExtraHeaders)

	requiresKey := preset.RequiresKey
	if provider == "custom" {
		requiresKey = apiKey != ""
	}

	return &Client{
		Provider:     provider,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		APIKey:       apiKey,
		Model:        model,
		Style:        style,
		RequiresKey:  requiresKey,
		ExtraHeaders: extraHeaders,
		HTTP:         &http.Client{Timeout: 10 * time.Minute},
	}
}

// parseHeaders parses "Name: Value, Other: Value2" into a header map.
// Each entry is split on the first colon so header values may contain colons.
func parseHeaders(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])
		if name != "" {
			out[name] = value
		}
	}
	return out
}

// Ready reports whether the client can make a request (i.e. required key set).
func (c *Client) Ready() (bool, string) {
	if c.RequiresKey && c.APIKey == "" {
		return false, fmt.Sprintf("API key required for provider %q. Set WATERWRITER_LLM_API_KEY (or use provider \"ollama\" / a custom local endpoint).", c.Provider)
	}
	if c.BaseURL == "" {
		return false, "no base URL configured. Set WATERWRITER_LLM_BASE_URL or choose a known provider."
	}
	if c.Model == "" {
		return false, "no model configured. Set WATERWRITER_LLM_MODEL."
	}
	return true, ""
}

// Complete returns the full (non-streamed) assistant message.
func (c *Client) Complete(ctx context.Context, messages []Message, temperature float64) (string, error) {
	if temperature == 0 {
		temperature = 0.7
	}
	switch c.Style {
	case StyleAnthropic:
		return c.anthropicComplete(ctx, messages, temperature, false)
	case StyleGemini:
		return c.geminiComplete(ctx, messages, temperature, false)
	default:
		return c.openAIComplete(ctx, messages, temperature, false)
	}
}

// CompleteStream streams the assistant message, invoking onChunk for each piece.
func (c *Client) CompleteStream(ctx context.Context, messages []Message, temperature float64, onChunk func(string) error) (string, error) {
	if temperature == 0 {
		temperature = 0.7
	}
	switch c.Style {
	case StyleAnthropic:
		return c.anthropicComplete(ctx, messages, temperature, true, onChunk)
	case StyleGemini:
		return c.geminiComplete(ctx, messages, temperature, true, onChunk)
	default:
		return c.openAIStream(ctx, messages, temperature, onChunk)
	}
}

// ---------- OpenAI-compatible (OpenAI, Gemini, DeepSeek, Groq, Ollama, ...) ----------

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) openAIComplete(ctx context.Context, messages []Message, temperature float64, _ bool) (string, error) {
	reqBody, _ := json.Marshal(chatRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setOpenAIHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func (c *Client) openAIStream(ctx context.Context, messages []Message, temperature float64, onChunk func(string) error) (string, error) {
	reqBody, _ := json.Marshal(chatRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setOpenAIHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var sb strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return sb.String(), err
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var sr struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &sr); err != nil {
			continue
		}
		if len(sr.Choices) > 0 {
			chunk := sr.Choices[0].Delta.Content
			if chunk != "" {
				sb.WriteString(chunk)
				if onChunk != nil {
					if err := onChunk(chunk); err != nil {
						return sb.String(), err
					}
				}
			}
		}
	}
	return sb.String(), nil
}

func (c *Client) setOpenAIHeaders(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	c.applyExtraHeaders(req)
}

func (c *Client) applyExtraHeaders(req *http.Request) {
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

// ---------- Anthropic (native /v1/messages) ----------

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

const anthropicVersion = "2023-06-01"

func (c *Client) anthropicComplete(ctx context.Context, messages []Message, temperature float64, stream bool, onChunk ...func(string) error) (string, error) {
	system, msgs := splitSystem(messages)
	// NOTE: Claude Sonnet 5 rejects non-default sampling parameters (temperature,
	// top_p, top_k) with a 400 error. Omit temperature for Anthropic so the model
	// uses its default. Older Claude models accept the default harmlessly.
	reqBody, _ := json.Marshal(anthropicRequest{
		Model:     c.Model,
		MaxTokens: 8192,
		System:    system,
		Messages:  msgs,
		Stream:    stream,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	c.applyExtraHeaders(req)

	if !stream {
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
		}
		var out anthropicResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		if out.Error != nil {
			return "", fmt.Errorf("llm error: %s", out.Error.Message)
		}
		var sb strings.Builder
		for _, b := range out.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return strings.TrimSpace(sb.String()), nil
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
	}
	defer resp.Body.Close()

	var cb func(string) error
	if len(onChunk) > 0 {
		cb = onChunk[0]
	}
	reader := bufio.NewReader(resp.Body)
	var sb strings.Builder
	var eventType string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return sb.String(), err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		if eventType == "content_block_delta" {
			var d struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(payload), &d); err != nil {
				continue
			}
			if d.Delta.Type == "text_delta" && d.Delta.Text != "" {
				sb.WriteString(d.Delta.Text)
				if cb != nil {
					if err := cb(d.Delta.Text); err != nil {
						return sb.String(), err
					}
				}
			}
		}
	}
	return sb.String(), nil
}

// splitSystem separates "system" messages (combined into one system string) from
// the rest, which Anthropic requires to use only "user"/"assistant" roles.
func splitSystem(messages []Message) (string, []anthropicMessage) {
	var systemParts []string
	var out []anthropicMessage
	for _, m := range messages {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "user", "assistant":
			out = append(out, anthropicMessage{Role: m.Role, Content: m.Content})
		default:
			// Unknown role -> treat as user to avoid API rejection.
			out = append(out, anthropicMessage{Role: "user", Content: m.Content})
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}

// ---------- Google Gemini (native Generative Language API, AI Studio) ----------

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role"` // "user" or "model"
	Parts []geminiPart `json:"parts"`
}

type geminiGenConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func geminiEndpoint(baseURL, model string, stream bool) string {
	if stream {
		return fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", baseURL, model)
	}
	return fmt.Sprintf("%s/v1beta/models/%s:generateContent", baseURL, model)
}

// buildGeminiRequest converts OpenAI-style messages into a Gemini request.
// System messages become the top-level systemInstruction; "assistant" is
// mapped to Gemini's "model" role.
func buildGeminiRequest(messages []Message, temperature float64) geminiRequest {
	var sys *geminiContent
	var contents []geminiContent
	for _, m := range messages {
		role := m.Role
		switch role {
		case "system":
			sys = &geminiContent{Role: "user", Parts: []geminiPart{{Text: m.Content}}}
			continue
		case "assistant":
			role = "model"
		case "user", "model":
			// keep as-is
		default:
			role = "user"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}
	return geminiRequest{
		SystemInstruction: sys,
		Contents:          contents,
		GenerationConfig:  geminiGenConfig{Temperature: temperature, MaxOutputTokens: 8192},
	}
}

func geminiText(resp geminiResponse) (string, error) {
	if resp.Error != nil {
		return "", fmt.Errorf("llm error: %s", resp.Error.Message)
	}
	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("llm returned no candidates")
	}
	var sb strings.Builder
	for _, p := range resp.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String()), nil
}

func (c *Client) geminiComplete(ctx context.Context, messages []Message, temperature float64, stream bool, onChunk ...func(string) error) (string, error) {
	reqBody, _ := json.Marshal(buildGeminiRequest(messages, temperature))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiEndpoint(c.BaseURL, c.Model, stream), bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	c.applyExtraHeaders(req)

	if !stream {
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
		}
		var out geminiResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		return geminiText(out)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return "", fmt.Errorf("llm request failed (%d): %s", resp.StatusCode, string(data))
	}
	defer resp.Body.Close()

	var cb func(string) error
	if len(onChunk) > 0 {
		cb = onChunk[0]
	}
	reader := bufio.NewReader(resp.Body)
	var sb strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return sb.String(), err
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var gr geminiResponse
		if err := json.Unmarshal([]byte(payload), &gr); err != nil {
			continue
		}
		text, perr := geminiText(gr)
		if perr != nil {
			// Gemini may emit partial chunks with no candidates during streaming;
			// ignore those rather than failing the whole request.
			continue
		}
		if text != "" {
			sb.WriteString(text)
			if cb != nil {
				if err := cb(text); err != nil {
					return sb.String(), err
				}
			}
		}
	}
	return sb.String(), nil
}

// ---------- Model discovery (live query of the provider) ----------

// ListModels returns the model IDs the configured provider exposes. It talks to
// each provider's models endpoint so users can discover what is available.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	switch c.Style {
	case StyleAnthropic:
		return c.listAnthropicModels(ctx)
	case StyleGemini:
		return c.listGeminiModels(ctx)
	default:
		return c.listOpenAIModels(ctx)
	}
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) listOpenAIModels(ctx context.Context) ([]string, error) {
	var url string
	if c.Provider == "ollama" {
		base := strings.TrimSuffix(c.BaseURL, "/v1")
		url = base + "/api/tags"
	} else {
		url = c.BaseURL + "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.Provider != "ollama" {
		c.setOpenAIHeaders(req)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("model list failed (%d): %s", resp.StatusCode, string(data))
	}

	if c.Provider == "ollama" {
		var out struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		out2 := make([]string, 0, len(out.Models))
		for _, m := range out.Models {
			out2 = append(out2, m.Name)
		}
		return out2, nil
	}

	var out openAIModelsResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	out2 := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		out2 = append(out2, m.ID)
	}
	return out2, nil
}

func (c *Client) listAnthropicModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	c.applyExtraHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("model list failed (%d): %s", resp.StatusCode, string(data))
	}
	var out openAIModelsResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	out2 := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		out2 = append(out2, m.ID)
	}
	return out2, nil
}

type geminiModelsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func (c *Client) listGeminiModels(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/v1beta/models?key=%s", c.BaseURL, c.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.applyExtraHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("model list failed (%d): %s", resp.StatusCode, string(data))
	}
	var out geminiModelsResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	const prefix = "models/"
	out2 := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		id := strings.TrimPrefix(m.Name, prefix)
		if id != "" {
			out2 = append(out2, id)
		}
	}
	return out2, nil
}
