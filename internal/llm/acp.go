package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// ACP (Agent Client Protocol) — JSON-RPC 2.0 over stdin/stdout
//
// This provider spawns the `opencode acp` subprocess and communicates using
// JSON-RPC 2.0. The flow for each request is:
//
//  1. initialize         — capability handshake (requires protocolVersion: 1)
//  2. session/new        — create a session (requires cwd + mcpServers)
//  3. session/prompt     — send a message (message is an ARRAY of messages)
//  4. session/close      — close the session
//  5. Kill the subprocess
//
// Tested against OpenCode ACP v1.18.4.
// ---------------------------------------------------------------------------

// jsonRPCReq is a JSON-RPC 2.0 request.
type jsonRPCReq struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

// jsonRPCResp is a JSON-RPC 2.0 response or notification.
type jsonRPCResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// acpComplete sends a buffered (non-streaming) message and returns the full
// assistant response text.
func (c *Client) acpComplete(ctx context.Context, messages []Message, _ float64) (string, error) {
	if c.Log != nil {
		c.Log.Info("ACP Complete: provider=%s model=%s", c.Provider, c.Model)
	}
	return c.acpDo(ctx, messages, false, nil)
}

// acpCompleteStream sends a streaming message and invokes onChunk for each
// partial text delta. Returns the full assembled text.
func (c *Client) acpCompleteStream(ctx context.Context, messages []Message, _ float64, onChunk func(string) error) (string, error) {
	if c.Log != nil {
		c.Log.Info("ACP streaming start: provider=%s model=%s", c.Provider, c.Model)
	}
	return c.acpDo(ctx, messages, true, onChunk)
}

// acpDo is the shared implementation for both buffered and streaming ACP calls.
func (c *Client) acpDo(ctx context.Context, messages []Message, stream bool, onChunk func(string) error) (string, error) {
	// 1. Resolve the opencode executable path.
	bin := "opencode"
	if c.ACPProcessPath != "" {
		bin = c.ACPProcessPath
	}

	// 2. Spawn the subprocess.
	// Capture opencode's stderr so subprocess crashes (e.g. the runtime
	// "slice bounds out of range [-2:]" error from opencode itself) are
	// logged for diagnosis instead of appearing directly on the user's
	// terminal as if Water Writer crashed.
	cmd := exec.CommandContext(ctx, bin, "acp")
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("acp start: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		// Log subprocess stderr on non-clean exit (exit code != 0).
		// The [-2:] error the user sees comes from opencode itself, not our code.
		if waitErr != nil {
			stderr := strings.TrimSpace(stderrBuf.String())
			if stderr != "" && c.Log != nil {
				c.Log.Warn("ACP subprocess stderr: %s", stderr)
			}
		}
	}()

	dec := json.NewDecoder(stdout)
	enc := json.NewEncoder(stdin)
	nextID := 1

	// We always stream, so collect chunks even for buffered calls.
	// However, we do NOT forward chunks to onChunk during the session because
	// OpenCode's streamed text is meta-commentary, not the actual output content.
	// After the session we'll read the output file and forward that instead.
	var allChunks []string
	chunkHandler := func(chunk string) error {
		allChunks = append(allChunks, chunk)
		return nil
	}

	// sendReq encodes a JSON-RPC request directly to stdin.
	sendReq := func(method string, params interface{}) (int, error) {
		id := nextID
		nextID++
		req := jsonRPCReq{
			JSONRPC: "2.0",
			Method:  method,
			Params:  params,
			ID:      id,
		}
		if err := enc.Encode(req); err != nil {
			return 0, fmt.Errorf("acp send %s: %w", method, err)
		}
		return id, nil
	}

	// readNext reads one JSON-RPC message from stdout using the shared decoder.
	readNext := func() (jsonRPCResp, error) {
		var resp jsonRPCResp
		if err := dec.Decode(&resp); err != nil {
			return jsonRPCResp{}, fmt.Errorf("acp read: %w", err)
		}
		return resp, nil
	}

	// waitForResponse reads messages until a response with the given ID arrives.
	// Notifications (no ID) are processed on the fly to collect text chunks.
	waitForResponse := func(wantID int) (jsonRPCResp, error) {
		for {
			resp, err := readNext()
			if err != nil {
				return resp, err
			}
			// Streaming notification — method "session/update" with text content.
			if resp.ID == nil && resp.Method == "session/update" {
				c.processStreamUpdate(resp.Params, chunkHandler)
				continue
			}
			// Ignore other notifications (no ID).
			if resp.ID == nil {
				continue
			}
			if *resp.ID == wantID {
				return resp, nil
			}
		}
	}

	// 3. Initialize — requires protocolVersion.
	if _, err := sendReq("initialize", map[string]interface{}{
		"protocolVersion": 1,
	}); err != nil {
		return "", err
	}
	if resp, err := waitForResponse(1); err != nil {
		return "", fmt.Errorf("acp initialize: %w", err)
	} else if resp.Error != nil {
		return "", fmt.Errorf("acp initialize error: %s", resp.Error.Message)
	}

	// 4. Create session — requires cwd and mcpServers.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
		if c.Log != nil {
			c.Log.Warn("ACP: os.Getwd() failed, using '.': %v", err)
		}
	}
	if _, err := sendReq("session/new", map[string]interface{}{
		"cwd":        cwd,
		"mcpServers": []interface{}{},
	}); err != nil {
		return "", err
	}

	var sessionResult struct {
		SessionID string `json:"sessionId"`
	}
	resp, err := waitForResponse(2)
	if err != nil {
		return "", fmt.Errorf("acp session/new: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("acp session/new error: %s (data: %s)", resp.Error.Message, string(resp.Error.Data))
	}
	if err := json.Unmarshal(resp.Result, &sessionResult); err != nil {
		return "", fmt.Errorf("acp session/new parse: %w", err)
	}
	sessionID := sessionResult.SessionID

	// 5. Create a temp directory for file output INSIDE the project directory.
	// OpenCode's session cwd is the project directory, so using a relative path
	// here lets OpenCode write files reliably (it couldn't write to /tmp).
	// The directory is prefixed with "." to keep it hidden from the user.
	workDir, err := os.MkdirTemp(cwd, ".waterwriter-acp-*")
	if err != nil {
		return "", fmt.Errorf("acp work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Use a relative path in the instruction since OpenCode's cwd is the project dir.
	relDir := filepath.Base(workDir)

	// 6. Send the conversation message via session/prompt.
	// ACP expects the "prompt" field to be an array of content blocks directly
	// (NOT messages with role wrappers). Each content block has "type" and "text".
	// Role info is passed separately via the "messages" parameter or inferred
	// by the server (all blocks are treated as user input).
	type acpBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	// Build content blocks from messages — we flatten system/user/assistant all
	// as user input since ACP doesn't use role-annotated messages.
	var blocks []acpBlock
	for _, m := range messages {
		if m.Content != "" {
			blocks = append(blocks, acpBlock{
				Type: "text",
				Text: m.Content,
			})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, acpBlock{
			Type: "text",
			Text: "Hello",
		})
	}

	// Embed the file instruction into the LAST user block so OpenCode sees it
	// as context for the task, not a separate instruction to act on.
	// We use a RELATIVE path to the work directory (in the project dir, same as
	// the ACP session's cwd) so OpenCode can write files reliably.
	fileInstr := fmt.Sprintf(
		"\n\n---\nOUTPUT FILE INSTRUCTION\n"+
			"Write your complete response to a new .md file inside this directory (relative to your current working directory):\n%s\n\n"+
			"Name the file descriptively based on the content/topic (e.g., \"chapter-1-subchapter-title.md\"). "+
			"Do NOT add extra files or folders beyond one output file. "+
			"The file content is what gets saved.\n",
		relDir,
	)
	if len(blocks) > 0 {
		blocks[len(blocks)-1].Text += fileInstr
	} else {
		blocks = append(blocks, acpBlock{Type: "text", Text: fileInstr})
	}

	// Always stream — ACP delivers text content exclusively through
	// session/update notifications. The final response only contains metadata
	// (stopReason, usage). For buffered calls we collect all chunks and
	// assemble the full text; for streaming calls we also forward each chunk.
	msgID, err := sendReq("session/prompt", map[string]interface{}{
		"sessionId": sessionID,
		"prompt":    blocks,
		"stream":    true, // always stream; text arrives via notifications
	})
	if err != nil {
		return "", err
	}

	resp, err = waitForResponse(msgID)
	if err != nil {
		return "", fmt.Errorf("acp session/prompt: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("acp session/prompt error: %s (data: %s)", resp.Error.Message, string(resp.Error.Data))
	}

	// Assemble final text from streaming chunks collected during waitForResponse.
	// The session/prompt response itself only contains metadata (stopReason, usage)
	// — the actual text content is delivered via session/update notifications.
	streamedText := strings.Join(allChunks, "")

	// 7. Close the session (best-effort).
	closeID := nextID
	if _, err := sendReq("session/close", map[string]interface{}{
		"sessionId": sessionID,
	}); err == nil {
		waitForResponse(closeID)
	}

	// 8. Read output files written by OpenCode in the work directory.
	// Instead of looking for a specific filename, we scan the entire workDir
	// for any files and use the one with the most content. This lets OpenCode
	// choose descriptive filenames (e.g., subchapter titles).
	finalText := streamedText
	hasFileOutput := false
	if fileContent := readBestFileFromDir(workDir); fileContent != "" {
		// Only use file content if it's longer than the streamed meta-commentary.
		// This avoids replacing a valid short response with an empty file.
		if len(fileContent) > len(streamedText) || len(fileContent) > 100 {
			finalText = fileContent
			hasFileOutput = true
			if c.Log != nil {
				c.Log.Info("ACP: read output file (%d chars), streamed=%d chars", len(fileContent), len(streamedText))
			}
		}
	}

	// Forward the actual output through onChunk (for streaming calls).
	// If we have file content, forward that instead of the meta-commentary.
	if stream && onChunk != nil {
		if hasFileOutput {
			// Forward file content in ~500-char chunks for live display.
			const chunkSize = 500
			for i := 0; i < len(finalText); i += chunkSize {
				end := i + chunkSize
				if end > len(finalText) {
					end = len(finalText)
				}
				if err := onChunk(finalText[i:end]); err != nil {
					break
				}
			}
		} else {
			// No file — fall back to forwarding the collected meta-commentary.
			for _, chunk := range allChunks {
				if err := onChunk(chunk); err != nil {
					break
				}
			}
		}
	}

	chars := len(finalText)
	if c.Log != nil {
		if stream {
			c.Log.Info("ACP streaming end: chars=%d", chars)
		} else {
			c.Log.Info("ACP response: chars=%d", chars)
		}
	}

	return strings.TrimSpace(finalText), nil
}

// processStreamUpdate attempts to extract text content from a session/update
// notification and forward it to onChunk.
func (c *Client) processStreamUpdate(params json.RawMessage, onChunk func(string) error) {
	if params == nil {
		return
	}

	// OpenCode ACP notification format (params has nested "update" field):
	// {
	//   "sessionId": "...",
	//   "update": {
	//     "sessionUpdate": "agent_message_chunk",
	//     "messageId": "...",
	//     "content": {"type": "text", "text": "..."}
	//   }
	// }
	var wrapper struct {
		Update *struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &wrapper); err == nil && wrapper.Update != nil {
		switch wrapper.Update.SessionUpdate {
		case "agent_message_chunk":
			if wrapper.Update.Content != nil && wrapper.Update.Content.Text != "" {
				onChunk(wrapper.Update.Content.Text)
				return
			}
		}
	}

	// Fallback: try direct text/delta fields for non-standard implementations.
	var s2 struct {
		Text   string `json:"text"`
		Delta  string `json:"delta"`
		Chunk  string `json:"chunk"`
		Update *struct {
			Content string `json:"content"`
			Text    string `json:"text"`
			Delta   string `json:"delta"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &s2); err == nil {
		chunk := s2.Text
		if chunk == "" {
			chunk = s2.Delta
		}
		if chunk == "" {
			chunk = s2.Chunk
		}
		if chunk == "" && s2.Update != nil {
			chunk = s2.Update.Content
			if chunk == "" {
				chunk = s2.Update.Text
			}
			if chunk == "" {
				chunk = s2.Update.Delta
			}
		}
		if chunk != "" {
			onChunk(chunk)
			return
		}
	}
}

// readBestFileFromDir scans a directory for all files (non-recursive), and
// returns the content of the one with the most content (preferring .md files).
// This lets OpenCode choose any descriptive filename for its output.
func readBestFileFromDir(dir string) string {
	dents, err := os.ReadDir(dir)
	if err != nil || len(dents) == 0 {
		return ""
	}

	var bestPath string
	var bestLen int

	for _, de := range dents {
		if de.IsDir() {
			continue
		}
		path := filepath.Join(dir, de.Name())
		info, err := de.Info()
		if err != nil {
			continue
		}
		// Skip very small files (< 20 bytes — likely placeholders or logs).
		if info.Size() < 20 {
			continue
		}
		if int(info.Size()) > bestLen {
			// Prefer .md files over other extensions when sizes are close.
			if strings.HasSuffix(de.Name(), ".md") || bestPath == "" || info.Size() > int64(bestLen)+100 {
				bestPath = path
				bestLen = int(info.Size())
			}
		}
	}

	if bestPath == "" {
		return ""
	}
	data, err := os.ReadFile(bestPath)
	if err != nil {
		return ""
	}
	// Delete the file immediately after reading — content is in the database now.
	os.Remove(bestPath)
	return strings.TrimSpace(string(data))
}

// acpListModels returns a placeholder since ACP has no model listing endpoint.
func (c *Client) acpListModels(_ context.Context) ([]string, error) {
	return []string{"opencode"}, nil
}
