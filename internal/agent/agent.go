package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/WaterEnterprises/WaterWriter/internal/llm"
	"github.com/WaterEnterprises/WaterWriter/internal/log"
)

type Agent struct {
	LLM *llm.Client
	DB  *db.DB
	Log *log.Logger
}

func New(llmClient *llm.Client, database *db.DB, logger *log.Logger) *Agent {
	if logger != nil {
		llmClient.Log = logger // Wire logger into the LLM client too.
	}
	return &Agent{LLM: llmClient, DB: database, Log: logger}
}

// trailingCommaRe matches a comma followed by optional whitespace and a
// closing bracket or brace — the classic LLM JSON trailing-comma bug.
var trailingCommaRe = regexp.MustCompile(`,(\s*[\]\}])`)

// cleanJSON fixes common LLM JSON formatting mistakes so Go's strict
// json.Unmarshal can parse it. Currently handles:
//   - Trailing commas: ["a", "b",] → ["a", "b"]
//   - Braces used as brackets: {"a", "b"} → ["a", "b"]
func cleanJSON(s string) string {
	s = trailingCommaRe.ReplaceAllString(s, "$1")
	// LLMs sometimes output {…} instead of […] for arrays (no key-value pairs).
	// If there are no colons outside quoted strings, convert braces to brackets.
	s = fixBraceArray(s)
	return s
}

// fixBraceArray detects cases where the LLM used {…} instead of […] for an
// array of strings. If the content has no colon outside of quoted strings,
// it's likely meant to be an array — replace outermost { } with [ ].
func fixBraceArray(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return s
	}
	inString := false
	hasColon := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inString = !inString
		}
		if !inString && s[i] == ':' {
			hasColon = true
			break
		}
	}
	if !hasColon {
		// Replace outermost braces with brackets.
		s = "[" + s[1:len(s)-1] + "]"
	}
	return s
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
		lines := strings.Split(s, "\n")
		var start int
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "```") || strings.HasPrefix(strings.TrimSpace(l), "~~~") {
				start = i + 1
				break
			}
		}
		var end int = len(lines)
		for i := len(lines) - 1; i > start; i-- {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") || strings.HasPrefix(strings.TrimSpace(lines[i]), "~~~") {
				end = i
				break
			}
		}
		if start < end {
			return cleanJSON(strings.TrimSpace(strings.Join(lines[start:end], "\n")))
		}
	}
	return cleanJSON(s)
}

func (a *Agent) GenerateQuestions(ctx context.Context, count int) ([]string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: generating %d interview questions", count)
	}
	sys := "You are the opening interviewer for the Water Writer AI book-writing agent. Generate thoughtful questions to understand what kind of book the user wants to write. Cover genre, target audience, core premise/theme, tone/style, length/scope, inspirations, and any must-include elements or constraints."
	user := fmt.Sprintf("Generate %d questions to ask the user about the book they want to write. Return ONLY a raw JSON array of strings with no markdown fences, no explanation.", count)

	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, 0.7)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: GenerateQuestions failed: %v", err)
		}
		return nil, fmt.Errorf("generate questions: %w", err)
	}
	result = extractJSON(result)

	var questions []string
	if err := json.Unmarshal([]byte(result), &questions); err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: failed to parse questions JSON: %v", err)
		}
		return nil, fmt.Errorf("parse questions: %w\nraw: %s", err, result)
	}
	if a.Log != nil {
		a.Log.Info("Agent: generated %d questions", len(questions))
	}
	return questions, nil
}

func (a *Agent) CompileBrief(ctx context.Context, pairs []db.QAPair) (string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: compiling brief from %d Q&A pairs", len(pairs))
	}
	var qaText strings.Builder
	qaText.WriteString("## User Interview\n\n")
	for i, p := range pairs {
		fmt.Fprintf(&qaText, "Q%d: %s\nA%d: %s\n\n", i+1, p.Question, i+1, p.Answer)
	}

	sys := "You are the research compiler for Water Writer. Given the questions and answers from the author interview, write a detailed, well-structured book brief in markdown. Capture the premise, audience, themes, tone, structure intent, and any constraints. This brief will guide the entire writing process."
	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: qaText.String()},
	}, 0.5)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: CompileBrief failed: %v", err)
		}
		return "", fmt.Errorf("compile brief: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("Agent: brief compiled successfully (%d chars)", len(result))
	}
	return result, nil
}

type TitleTOCResponse struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Chapters []string `json:"chapters"`
}

func (a *Agent) GenerateTitleTOC(ctx context.Context, brief string) (*TitleTOCResponse, error) {
	if a.Log != nil {
		a.Log.Info("Agent: generating title and TOC")
	}
	sys := "You are the book architect for Water Writer. Given the book brief, produce a compelling title, an optional subtitle, and a table of contents as an ordered list of chapter titles. Return ONLY raw JSON with the schema {\"title\":string,\"subtitle\":string,\"chapters\":[string,...]}. No markdown fences."
	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: brief},
	}, 0.7)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: GenerateTitleTOC failed: %v", err)
		}
		return nil, fmt.Errorf("generate title/toc: %w", err)
	}
	result = extractJSON(result)

	var resp TitleTOCResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: failed to parse title/TOC JSON: %v", err)
		}
		return nil, fmt.Errorf("parse title/toc: %w\nraw: %s", err, result)
	}
	if a.Log != nil {
		a.Log.Info("Agent: title=%q chapters=%d", resp.Title, len(resp.Chapters))
	}
	return &resp, nil
}

func (a *Agent) GenerateSubchapters(ctx context.Context, brief string, chapterTitle string) ([]string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: generating subchapters for %q", chapterTitle)
	}
	user := fmt.Sprintf("Book Brief:\n%s\n\nChapter: %s\n\nGenerate 3-6 subchapter titles that develop this chapter logically. Return ONLY a raw JSON array of strings. No markdown fences.", brief, chapterTitle)
	sys := "You are the outline editor for Water Writer. Given the book brief and a specific chapter title, produce a list of subchapter titles (sections) for that chapter."
	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, 0.7)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: GenerateSubchapters for %q failed: %v", chapterTitle, err)
		}
		return nil, fmt.Errorf("generate subchapters: %w", err)
	}
	result = extractJSON(result)

	var subs []string
	if err := json.Unmarshal([]byte(result), &subs); err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: failed to parse subchapters JSON: %v", err)
		}
		return nil, fmt.Errorf("parse subchapters: %w\nraw: %s", err, result)
	}
	if a.Log != nil {
		a.Log.Info("Agent: generated %d subchapters for %q", len(subs), chapterTitle)
	}
	return subs, nil
}

func (a *Agent) BuildWriteContext(ctx context.Context, projectID int, chapter *db.Chapter, subchapter *db.Subchapter) (string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: building write context for %q / %q", chapter.Title, subchapter.Title)
	}
	brief, err := a.DB.GetBrief(projectID)
	if err != nil {
		return "", fmt.Errorf("get brief: %w", err)
	}
	book, err := a.DB.GetBook(projectID)
	if err != nil {
		return "", fmt.Errorf("get book: %w", err)
	}
	chapters, err := a.DB.GetChapters(projectID)
	if err != nil {
		return "", fmt.Errorf("get chapters: %w", err)
	}
	allSubs, err := a.DB.GetAllSubchapters(projectID)
	if err != nil {
		return "", fmt.Errorf("get subchapters: %w", err)
	}

	var priorContent strings.Builder
	priorContent.WriteString("## Previously Written Book Content\n\n")
	for _, ch := range chapters {
		fmt.Fprintf(&priorContent, "### %s\n\n", ch.Title)
		for _, s := range allSubs {
			if s.ChapterID == ch.ID && s.Status == "done" && s.Content != "" {
				fmt.Fprintf(&priorContent, "#### %s\n\n%s\n\n", s.Title, s.Content)
			}
		}
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("## Book Brief\n\n")
	contextBuilder.WriteString(brief)
	contextBuilder.WriteString("\n\n")
	fmt.Fprintf(&contextBuilder, "## Title: %s\n", book.Title)
	if book.Subtitle != "" {
		fmt.Fprintf(&contextBuilder, "## Subtitle: %s\n", book.Subtitle)
	}
	contextBuilder.WriteString("\n")
	contextBuilder.WriteString("## Table of Contents\n\n")
	for i, ch := range chapters {
		fmt.Fprintf(&contextBuilder, "%d. %s\n", i+1, ch.Title)
	}
	contextBuilder.WriteString("\n")
	contextBuilder.WriteString(priorContent.String())
	contextBuilder.WriteString("\n")
	fmt.Fprintf(&contextBuilder, "## Current Assignment\n\n")
	fmt.Fprintf(&contextBuilder, "Write the subchapter titled **%s** (Chapter: %s).\n\n", subchapter.Title, chapter.Title)
	contextBuilder.WriteString("Maintain consistent voice, style, and continuity. Write in rich markdown prose. Aim for 800-1500 words. Do not repeat previous content. End naturally.")

	return contextBuilder.String(), nil
}

// TranslateTitleTOC translates the book title, subtitle, and all chapter titles
// into the target language in a single LLM call for context consistency.
func (a *Agent) TranslateTitleTOC(ctx context.Context, language string, bookTitle, subtitle string, chapters []*db.Chapter) ([]string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: translating title/TOC to %q", language)
	}
	var tocBuilder strings.Builder
	fmt.Fprintf(&tocBuilder, "Title: %s\n", bookTitle)
	if subtitle != "" {
		fmt.Fprintf(&tocBuilder, "Subtitle: %s\n", subtitle)
	}
	tocBuilder.WriteString("Chapters:\n")
	for i, ch := range chapters {
		fmt.Fprintf(&tocBuilder, "%d. %s\n", i+1, ch.Title)
	}

	sys := fmt.Sprintf("You are a professional literary translator. Translate the following book title, subtitle, and chapter titles into %s. Preserve the tone, style, and cultural nuances. Return ONLY a JSON object with keys: title (string), subtitle (string, empty if none), chapters (array of strings in the same order). No markdown fences.", language)
	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: tocBuilder.String()},
	}, 0.3)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: TranslateTitleTOC failed: %v", err)
		}
		return nil, fmt.Errorf("translate title/toc: %w", err)
	}
	result = extractJSON(result)

	var resp struct {
		Title    string   `json:"title"`
		Subtitle string   `json:"subtitle"`
		Chapters []string `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: failed to parse translated title/TOC JSON: %v", err)
		}
		return nil, fmt.Errorf("parse translated title/toc: %w\nraw: %s", err, result)
	}
	if a.Log != nil {
		a.Log.Info("Agent: translated title=%q subtitle=%q chapters=%d", resp.Title, resp.Subtitle, len(resp.Chapters))
	}
	// Return [title, subtitle, chapters...] for convenience.
	out := []string{resp.Title, resp.Subtitle}
	out = append(out, resp.Chapters...)
	return out, nil
}

// TranslateSubchapterContent translates a single subchapter's content into the
// target language, with the book context provided for consistency.
func (a *Agent) TranslateSubchapterContent(ctx context.Context, language, bookTitle, subtitle, chapterTitle, subchapterTitle, content string) (string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: translating subchapter %q / %q to %q (%d chars)", chapterTitle, subchapterTitle, language, len(content))
	}
	user := fmt.Sprintf("Book: %s\n", bookTitle)
	if subtitle != "" {
		user += fmt.Sprintf("Subtitle: %s\n", subtitle)
	}
	user += fmt.Sprintf("Chapter: %s\n", chapterTitle)
	user += fmt.Sprintf("Section: %s\n\n", subchapterTitle)
	user += content

	sys := fmt.Sprintf("You are a professional literary translator. Translate the following book section into %s. Preserve the author's voice, tone, style, and cultural references. Do NOT add any notes, commentary, or markdown formatting. Translate only the text content. Keep paragraph structure intact.", language)
	result, err := a.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, 0.3)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: TranslateSubchapterContent for %q / %q failed: %v", chapterTitle, subchapterTitle, err)
		}
		return "", fmt.Errorf("translate subchapter: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("Agent: translated subchapter successfully (%d chars)", len(result))
	}
	return result, nil
}

// TranslateSubchapterContentStream translates a subchapter's content into the
// target language with the book context, streaming chunks via onChunk.
func (a *Agent) TranslateSubchapterContentStream(ctx context.Context, language, bookTitle, subtitle, chapterTitle, subchapterTitle, content string, onChunk func(string) error) (string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: streaming translation of subchapter %q / %q to %q (%d chars)", chapterTitle, subchapterTitle, language, len(content))
	}
	user := fmt.Sprintf("Book: %s\n", bookTitle)
	if subtitle != "" {
		user += fmt.Sprintf("Subtitle: %s\n", subtitle)
	}
	user += fmt.Sprintf("Chapter: %s\n", chapterTitle)
	user += fmt.Sprintf("Section: %s\n\n", subchapterTitle)
	user += content

	sys := fmt.Sprintf("You are a professional literary translator. Translate the following book section into %s. Preserve the author's voice, tone, style, and cultural references. Do NOT add any notes, commentary, or markdown formatting. Translate only the text content. Keep paragraph structure intact.", language)
	result, err := a.LLM.CompleteStream(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, 0.3, onChunk)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: TranslateSubchapterContentStream for %q / %q failed: %v", chapterTitle, subchapterTitle, err)
		}
		return result, fmt.Errorf("translate subchapter stream: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("Agent: streaming translated subchapter successfully (%d chars)", len(result))
	}
	return result, nil
}

func (a *Agent) WriteSubchapterStream(ctx context.Context, prompt string, onChunk func(string) error) (string, error) {
	if a.Log != nil {
		a.Log.Info("Agent: writing subchapter stream")
	}
	sys := "You are the prose writer for Water Writer. Write the content for a single subchapter of a book. Maintain consistent voice and continuity with previously written material. Write in markdown. Do not add meta commentary or notes to the author. End the section naturally."
	content, err := a.LLM.CompleteStream(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: prompt},
	}, 0.8, onChunk)
	if err != nil {
		if a.Log != nil {
			a.Log.Error("Agent: WriteSubchapterStream failed: %v", err)
		}
		return content, fmt.Errorf("write subchapter: %w", err)
	}
	if a.Log != nil {
		a.Log.Info("Agent: subchapter written (%d chars)", len(content))
	}
	return content, nil
}
