package db

import (
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestPhaseTransitions(t *testing.T) {
	d := newTestDB(t)
	p, err := d.CreateProject("Book")
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID

	if got := d.GetPhase(id); got != "qa" {
		t.Fatalf("phase after create = %q, want qa", got)
	}

	// qa -> questions + pairs
	if err := d.SaveQAQuestions(id, []string{"Q1?", "Q2?", "Q3?"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveQAPair(id, "Q1?", "A1", 1); err != nil {
		t.Fatal(err)
	}
	if got := d.GetPhase(id); got != "qa" {
		t.Fatalf("phase with partial answers = %q, want qa", got)
	}
	d.SaveQAPair(id, "Q2?", "A2", 2)
	d.SaveQAPair(id, "Q3?", "A3", 3)
	pairs, _ := d.GetQAPairs(id)
	if len(pairs) != 3 {
		t.Fatalf("got %d qa pairs, want 3", len(pairs))
	}

	// qa -> brief compiled -> next action is titletoc
	if err := d.SaveBrief(id, "the brief"); err != nil {
		t.Fatal(err)
	}
	if got := d.GetPhase(id); got != "titletoc" {
		t.Fatalf("phase after brief = %q, want titletoc", got)
	}

	// titletoc -> books + chapters
	book, err := d.SaveBook(id, "Title", "Sub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveChapter(book.ID, id, 1, "Chapter 1"); err != nil {
		t.Fatal(err)
	}
	if got := d.GetPhase(id); got != "subchapters" {
		t.Fatalf("phase after book = %q, want subchapters", got)
	}

	// subchapters -> writing (pending subchapter)
	chapters, _ := d.GetChapters(id)
	ch := chapters[0]
	sub, err := d.SaveSubchapter(ch.ID, book.ID, id, 1, "Sub A")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.GetPhase(id); got != "writing" {
		t.Fatalf("phase after subchapter = %q, want writing", got)
	}

	// subchapters -> writing (pending subchapter)
	if got := d.GetPhase(id); got != "writing" {
		t.Fatalf("phase with pending sub = %q, want writing", got)
	}

	// writing -> done
	if err := d.UpdateSubchapterContent(sub.ID, "content"); err != nil {
		t.Fatal(err)
	}
	if got := d.GetPhase(id); got != "done" {
		t.Fatalf("phase after writing = %q, want done", got)
	}
}

func TestSettings(t *testing.T) {
	d := newTestDB(t)
	if v, _ := d.GetSetting("provider"); v != "" {
		t.Fatalf("expected empty provider, got %q", v)
	}
	if err := d.SetSetting("provider", "gemini"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSetting("model", "gemini-3.5-flash"); err != nil {
		t.Fatal(err)
	}
	// overwrite
	if err := d.SetSetting("provider", "openai"); err != nil {
		t.Fatal(err)
	}
	s, err := d.GetSettings("provider", "model")
	if err != nil {
		t.Fatal(err)
	}
	if s["provider"] != "openai" {
		t.Fatalf("provider = %q, want openai", s["provider"])
	}
	if s["model"] != "gemini-3.5-flash" {
		t.Fatalf("model = %q, want gemini-3.5-flash", s["model"])
	}
	if err := d.SetSettings(map[string]string{"style": "openai", "base_url": "http://x"}); err != nil {
		t.Fatal(err)
	}
	s, _ = d.GetSettings("style", "base_url")
	if s["style"] != "openai" || s["base_url"] != "http://x" {
		t.Fatalf("settings = %+v", s)
	}
}

func TestTodosAndResume(t *testing.T) {
	d := newTestDB(t)
	p, _ := d.CreateProject("B2")
	id := p.ID

	questions := []string{"Q1?", "Q2?"}
	if err := d.SaveQAQuestions(id, questions); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetQAQuestions(id)
	if err != nil || len(got) != 2 {
		t.Fatalf("GetQAQuestions = %v, %v", got, err)
	}

	// Simulate answering one question, then "resuming".
	d.SaveQAPair(id, "Q1?", "A1", 1)
	remaining := len(got) - 1
	if remaining != 1 {
		t.Fatalf("expected 1 remaining question, got %d", remaining)
	}
	pairs, _ := d.GetQAPairs(id)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 saved answer, got %d", len(pairs))
	}

	// Todos
	book, _ := d.SaveBook(id, "T", "")
	ch, _ := d.SaveChapter(book.ID, id, 1, "C1")
	if _, err := d.SaveTodo(id, "chapter", ch.ID, "Write chapter: C1"); err != nil {
		t.Fatal(err)
	}
	sub, _ := d.SaveSubchapter(ch.ID, book.ID, id, 1, "S1")
	if _, err := d.SaveTodo(id, "subchapter", sub.ID, "Write S1"); err != nil {
		t.Fatal(err)
	}
	todos, err := d.GetTodos(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(todos))
	}
	if err := d.MarkTodoDoneByRef("subchapter", sub.ID); err != nil {
		t.Fatal(err)
	}
	todos, err = d.GetTodos(id)
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	for _, td := range todos {
		if td.Done {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("expected 1 done todo, got %d", done)
	}
}
