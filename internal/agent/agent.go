package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
			return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		}
	}
	return s
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
