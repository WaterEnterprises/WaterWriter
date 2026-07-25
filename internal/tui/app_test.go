package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WaterEnterprises/WaterWriter/internal/agent"
	"github.com/WaterEnterprises/WaterWriter/internal/db"
	tea "github.com/charmbracelet/bubbletea"
)

// isQuitCmd executes a command and checks if it returns tea.QuitMsg.
// tea.Quit is a function (Cmd = func() tea.Msg), which cannot be compared
// with == in Go. This helper runs the command and inspects the result.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func newTestAgent(t *testing.T) *agent.Agent {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return agent.New(nil, database, nil)
}

func TestQKeyDoesNotQuitInQAState(t *testing.T) {
	ag := newTestAgent(t)
	_, err := ag.DB.CreateProject("Test")
	if err != nil {
		t.Fatal(err)
	}

	m := NewModel(ag, &db.Project{Name: "Test"}, nil)
	m.state = stateQA
	m.questions = []string{"What is your book about?"}
	m.qaIndex = 0

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	result, cmd := m.Update(keyMsg)

	if isQuitCmd(cmd) {
		t.Fatal("pressing 'q' in QA state should not quit the app")
	}
	updated := result.(Model)
	if updated.state != stateQA {
		t.Fatalf("expected state stateQA, got %v", updated.state)
	}
	if updated.input.Value() != "q" {
		t.Fatalf("expected input value 'q', got %q", updated.input.Value())
	}
}

func TestQKeyQuitsInNonInputStates(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateThink

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	_, cmd := m.Update(keyMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected 'q' in stateThink to quit, but got a different command")
	}
}

func TestEnterBlockedWithin50msOfLastChar(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateQA
	m.questions = []string{"What is your book about?"}
	m.qaIndex = 0
	m.qaLastChar = time.Now().Add(-10 * time.Millisecond)
	m.input.SetValue("Some pasted text")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd != nil {
		t.Fatal("expected Enter within 50ms of last char to be ignored (nil cmd), but got a command")
	}
	updated := result.(Model)
	if updated.state != stateQA {
		t.Fatalf("expected state stateQA after ignored Enter, got %v", updated.state)
	}
	if updated.input.Value() != "Some pasted text" {
		t.Fatalf("expected input to be unchanged after ignored Enter, got %q", updated.input.Value())
	}
}

func TestEnterNotBlockedAfter50ms(t *testing.T) {
	ag := newTestAgent(t)
	proj, err := ag.DB.CreateProject("TestBook")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.SaveQAQuestions(proj.ID, []string{"Q1"})

	m := NewModel(ag, proj, nil)
	m.state = stateQA
	m.questions = []string{"Q1"}
	m.qaIndex = 0
	m.qaLastChar = time.Now().Add(-200 * time.Millisecond)
	m.input.SetValue("My answer")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	_, cmd := m.Update(enterMsg)

	if cmd == nil {
		t.Fatal("expected Enter after >50ms of last char to be processed (non-nil cmd), but got nil")
	}
	// Verify it returns a qaSavedMsg (the async save command)
	result := cmd()
	if _, ok := result.(qaSavedMsg); !ok {
		t.Fatalf("expected save command to return qaSavedMsg, got %T", result)
	}
}

func TestQALastCharUpdatedOnKeyEvent(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateQA
	m.questions = []string{"Q?"}
	m.qaIndex = 0
	m.qaLastChar = time.Time{} // zero value

	charMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}
	result, _ := m.Update(charMsg)
	updated := result.(Model)

	if updated.qaLastChar.IsZero() {
		t.Fatal("expected qaLastChar to be updated after a key event, but it's still zero")
	}
}

func TestEnterNotBlockedWhenNotPasting(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateQA
	m.questions = []string{"What is your book about?"}
	m.qaIndex = 0
	m.input.SetValue("")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd for empty answer submit")
	}
	updated := result.(Model)
	if updated.state != stateQA {
		t.Fatalf("expected state stateQA after empty submit, got %v", updated.state)
	}
}

func TestEnterWithAnswerReturnsAsyncSaveCmd(t *testing.T) {
	ag := newTestAgent(t)
	proj, err := ag.DB.CreateProject("TestBook")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.SaveQAQuestions(proj.ID, []string{"Q1", "Q2"})

	m := NewModel(ag, proj, nil)
	m.state = stateQA
	m.questions = []string{"Q1", "Q2"}
	m.qaIndex = 0
	m.input.SetValue("My answer")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	// A save command should be returned (async DB write).
	if cmd == nil {
		t.Fatal("expected a non-nil save command for non-empty answer")
	}

	updated := result.(Model)

	// The UI should have advanced to the next question immediately.
	if updated.qaIndex != 1 {
		t.Fatalf("expected qaIndex 1 (advanced), got %d", updated.qaIndex)
	}
	if updated.input.Value() != "" {
		t.Fatalf("expected input to be cleared after submit, got %q", updated.input.Value())
	}
	if updated.state != stateQA {
		t.Fatalf("expected state to be stateQA (next question), got %v", updated.state)
	}

	// In-memory qaPairs should already contain the answer.
	if len(updated.qaPairs) != 1 {
		t.Fatalf("expected 1 qaPair in memory, got %d", len(updated.qaPairs))
	}
	if updated.qaPairs[0].Answer != "My answer" {
		t.Fatalf("expected in-memory answer 'My answer', got %q", updated.qaPairs[0].Answer)
	}

	// Execute the save command and verify it returns qaSavedMsg.
	saveResult := cmd()
	if _, ok := saveResult.(qaSavedMsg); !ok {
		t.Fatalf("expected save command to return qaSavedMsg, got %T", saveResult)
	}
}

func TestEnterWithAnswerOnLastQuestionGoesToReview(t *testing.T) {
	ag := newTestAgent(t)
	proj, err := ag.DB.CreateProject("TestBook")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.SaveQAQuestions(proj.ID, []string{"Only question"})

	m := NewModel(ag, proj, nil)
	m.state = stateQA
	m.questions = []string{"Only question"}
	m.qaIndex = 0
	m.input.SetValue("Final answer")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd == nil {
		t.Fatal("expected a non-nil save command for non-empty answer")
	}

	updated := result.(Model)

	// Should go to review screen immediately.
	if updated.state != stateQAReview {
		t.Fatalf("expected state to be stateQAReview (last question), got %v", updated.state)
	}

	// In-memory qaPairs should already contain the answer.
	if len(updated.qaPairs) != 1 {
		t.Fatalf("expected 1 qaPair in memory, got %d", len(updated.qaPairs))
	}
	if updated.qaPairs[0].Answer != "Final answer" {
		t.Fatalf("expected in-memory answer 'Final answer', got %q", updated.qaPairs[0].Answer)
	}

	// Execute the save command and verify it returns qaSavedMsg.
	saveResult := cmd()
	if _, ok := saveResult.(qaSavedMsg); !ok {
		t.Fatalf("expected save command to return qaSavedMsg, got %T", saveResult)
	}

	// Also verify the data was actually saved to the DB.
	pairs, err := ag.DB.GetQAPairs(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 qaPair in DB, got %d", len(pairs))
	}
	if pairs[0].Answer != "Final answer" {
		t.Fatalf("expected DB answer 'Final answer', got %q", pairs[0].Answer)
	}
}

func TestEnterOnErrorGoesToHome(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateError
	m.err = fmt.Errorf("test error")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd (not tea.Quit) when pressing Enter on error screen, got a command")
	}
	updated := result.(Model)
	if updated.state != stateHome {
		t.Fatalf("expected state to be stateHome after Enter on error, got %v", updated.state)
	}
}

func TestQQuitsFromError(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateError
	m.err = fmt.Errorf("test error")

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	_, cmd := m.Update(keyMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected 'q' on error screen to quit, but got a different command")
	}
}

func TestCtrlCQuitsFromError(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateError
	m.err = fmt.Errorf("test error")

	keyMsg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.Update(keyMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected Ctrl+C on error screen to quit, but got a different command")
	}
}

func TestErrorViewShowsError(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateError
	m.err = fmt.Errorf("API call failed: 503 Service Unavailable")
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "API call failed: 503 Service Unavailable") {
		t.Fatal("expected error view to contain the error message")
	}
	if !strings.Contains(view, "[Enter]") {
		t.Fatal("expected error view to show '[Enter]' instruction for going back")
	}
	if !strings.Contains(view, "[Ctrl+C]") {
		t.Fatal("expected error view to show '[Ctrl+C]' option for quitting")
	}
}

func TestCtrlCAlwaysQuits(t *testing.T) {
	m := NewModel(nil, &db.Project{Name: "Test"}, nil)
	m.state = stateQA
	m.questions = []string{"What is your book about?"}
	m.qaIndex = 0

	keyMsg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.Update(keyMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected Ctrl+C in QA state to quit, but got a different command")
	}
}

// TestHomeExportFlow simulates the full home-screen export flow:
// 1. Open a project database with existing data
// 2. Navigate to a project in the list
// 3. Press 'e' to start export
// 4. Enter a path and press Enter
// 5. Verify the markdown file is created with correct content
//
// This test also covers:
// - The Esc cancel flow (cancel path entry)
// - Exporting a project without a book (just a project name)
func TestHomeExportFlow(t *testing.T) {
	ag := newTestAgent(t)
	_, err := ag.DB.CreateProject("ExportTest")
	if err != nil {
		t.Fatal(err)
	}
	// Create a book with chapters and subchapters.
	book, err := ag.DB.SaveBook(1, "Export Test Book", "A Test Subtitle")
	if err != nil {
		t.Fatal(err)
	}
	ch1, err := ag.DB.SaveChapter(book.ID, 1, 1, "Introduction")
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := ag.DB.SaveChapter(book.ID, 1, 2, "Deep Dive")
	if err != nil {
		t.Fatal(err)
	}
	// Add subchapters with content.
	_, err = ag.DB.SaveSubchapter(ch1.ID, book.ID, 1, 1, "Welcome")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.UpdateSubchapterContent(1, "Welcome paragraph content.")
	_, err = ag.DB.SaveSubchapter(ch1.ID, book.ID, 1, 2, "Getting Started")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.UpdateSubchapterContent(2, "Getting started content here.")
	_, err = ag.DB.SaveSubchapter(ch2.ID, book.ID, 1, 1, "Advanced Topics")
	if err != nil {
		t.Fatal(err)
	}
	ag.DB.UpdateSubchapterContent(3, "Advanced topics content.")

	// Create the home model with projects pre-loaded.
	m := NewHomeModel(ag, nil, true, "")
	m.width = 80
	m.height = 24
	m.projects = []*db.Project{{ID: 1, Name: "ExportTest"}}
	m.cursor = 1 // cursor on the project (index 1 = first project)

	// --- Step 1: Press 'e' to start export ---
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	result, cmd := m.Update(keyMsg)
	_ = cmd

	m2 := result.(Model)
	if !m2.homeExporting {
		t.Fatal("expected homeExporting to be true after pressing 'e' on a project")
	}
	if m2.homeExportResult != "" {
		t.Fatal("expected homeExportResult to be empty when starting export")
	}
	if m2.input.Value() != "." {
		t.Fatalf("expected input default value '.', got %q", m2.input.Value())
	}
	if m2.project.ID != 1 {
		t.Fatalf("expected project.ID 1, got %d", m2.project.ID)
	}

	// --- Step 2: Press Esc to cancel (test cancel flow) ---
	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	result, cmd = m2.Update(escMsg)
	_ = cmd
	m3 := result.(Model)
	if m3.homeExporting {
		t.Fatal("expected homeExporting to be false after pressing Esc")
	}

	// --- Step 3: Press 'e' again to restart export ---
	result, cmd = m3.Update(keyMsg)
	_ = cmd
	m4 := result.(Model)
	if !m4.homeExporting {
		t.Fatal("expected homeExporting to be true after pressing 'e' again")
	}

	// --- Step 4: Press Enter to save path and advance to subs choice ---
	exportDir := filepath.Join(t.TempDir(), "export_test")
	m4.homeExportPath = exportDir
	m4.input.SetValue(exportDir)
	m4.homeExporting = true
	m4.homeExportStep = 1

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd = m4.Update(enterMsg)
	_ = cmd
	m5 := result.(Model)

	if !m5.homeExporting {
		t.Fatal("expected homeExporting to still be true (step 2: subs choice)")
	}
	if m5.homeExportStep != 2 {
		t.Fatal("expected homeExportStep to be 2 after first Enter")
	}

	// --- Step 5: Press Enter to confirm subs choice, advance to format choice ---
	result, cmd = m5.Update(enterMsg)
	_ = cmd
	m6 := result.(Model)

	if !m6.homeExporting {
		t.Fatal("expected homeExporting to still be true (step 3: format choice)")
	}
	if m6.homeExportStep != 3 {
		t.Fatal("expected homeExportStep to be 3 after second Enter")
	}

	// --- Step 6: Press Enter again to confirm format (default: markdown) and fire export ---
	result, cmd = m6.Update(enterMsg)
	_ = cmd
	m7 := result.(Model)

	if m7.homeExporting {
		t.Fatal("expected homeExporting to be false after pressing Enter to export")
	}

	// The Enter key went through handleHomeKey which fires exportHomeBook().
	// Execute the export command to actually write the file.
	if cmd != nil {
		// Execute the command closure to get the exportDoneMsg.
		exportResult := cmd()
		doneMsg, ok := exportResult.(exportDoneMsg)
		if !ok {
			t.Fatalf("expected exportDoneMsg from exportHomeBook, got %T", exportResult)
		}
		if doneMsg.err != nil {
			t.Fatalf("export failed: %v", doneMsg.err)
		}
		if doneMsg.path == "" {
			t.Fatal("expected non-empty export path")
		}

		// Verify the exported file exists and has the correct content.
		bookContent, err := os.ReadFile(doneMsg.path)
		if err != nil {
			t.Fatalf("read exported file: %v", err)
		}
		content := string(bookContent)

		// Check title.
		if !strings.Contains(content, "# Export Test Book") {
			t.Fatal("expected exported file to contain '# Export Test Book'")
		}
		// Check subtitle.
		if !strings.Contains(content, "## A Test Subtitle") {
			t.Fatal("expected exported file to contain '## A Test Subtitle'")
		}
		// Check chapter numbering.
		if !strings.Contains(content, "## Chapter 1: Introduction") {
			t.Fatal("expected exported file to contain '## Chapter 1: Introduction'")
		}
		if !strings.Contains(content, "## Chapter 2: Deep Dive") {
			t.Fatal("expected exported file to contain '## Chapter 2: Deep Dive'")
		}
		// Check subchapter content.
		if !strings.Contains(content, "### Welcome") {
			t.Fatal("expected exported file to contain '### Welcome'")
		}
		if !strings.Contains(content, "Welcome paragraph content.") {
			t.Fatal("expected exported file to contain subchapter content")
		}
		if !strings.Contains(content, "### Advanced Topics") {
			t.Fatal("expected exported file to contain '### Advanced Topics'")
		}
		if !strings.Contains(content, "Advanced topics content.") {
			t.Fatal("expected exported file to contain advanced topics content")
		}

		// Verify no duplicates in chapter headings — "Chapter 1:" appears twice:
		// once in the Table of Contents and once as the actual heading.
		// Count only the `## Chapter 1:` heading marker.
		if strings.Count(content, "## Chapter 1:") != 1 {
			t.Fatal("expected exactly one chapter heading '## Chapter 1:' (no duplicates)")
		}
		// TOC should have the link entry.
		if strings.Count(content, "[Chapter 1: Introduction]") != 1 {
			t.Fatal("expected TOC link 'Chapter 1: Introduction' in the Table of Contents")
		}
		if strings.Count(content, "## Table of Contents") != 1 {
			t.Fatal("expected '## Table of Contents' section in the export")
		}

		// Also verify the result through the model by sending the exportDoneMsg.
		result, _ = m7.Update(doneMsg)
		m8 := result.(Model)
		if m8.homeExportResult != doneMsg.path {
			t.Fatalf("expected homeExportResult to be set to the export path")
		}
		if m8.homeExportError != nil {
			t.Fatalf("expected homeExportError to be nil, got %v", m8.homeExportError)
		}

		// --- Step 7: Press Enter on the result overlay to go back ---
		enterMsg2 := tea.KeyMsg{Type: tea.KeyEnter}
		result, cmd = m8.Update(enterMsg2)
		_ = cmd
		m9 := result.(Model)
		if m9.homeExportResult != "" {
			t.Fatal("expected homeExportResult to be cleared after pressing Enter on result")
		}
		if m9.homeExportError != nil {
			t.Fatal("expected homeExportError to be cleared after pressing Enter on result")
		}
	} else {
		t.Fatal("expected a non-nil cmd from the export flow")
	}
}

// TestHomeExportFlowNoBook tests exporting a project that has no book data yet
// (e.g., still in Q&A phase). It should export with just the project name as title.
func TestHomeExportFlowNoBook(t *testing.T) {
	ag := newTestAgent(t)
	proj, err := ag.DB.CreateProject("NoBookTest")
	if err != nil {
		t.Fatal(err)
	}

	// Create home model with just the project name (no book, chapters, or subchapters).
	m := NewHomeModel(ag, nil, true, "")
	m.width = 80
	m.height = 24
	m.project = proj
	m.projects = []*db.Project{proj}
	m.cursor = 1

	// Start export flow.
	exportDir := filepath.Join(t.TempDir(), "export_nobook")
	m.homeExporting = true
	m.homeExportPath = exportDir
	m.input.SetValue(exportDir)
	m.homeExportStep = 1

	// Step 1: Save path and advance to subs choice.
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)
	_ = result
	m2 := result.(Model)

	// Step 2: Confirm subs choice, advance to format choice.
	result, cmd = m2.Update(enterMsg)
	_ = result
	m3 := result.(Model)

	// Step 3: Confirm format (default: markdown) and fire export.
	result, cmd = m3.Update(enterMsg)
	_ = result

	if cmd == nil {
		t.Fatal("expected non-nil cmd from export on project without book")
	}

	// Execute the export command.
	exportResult := cmd()
	doneMsg, ok := exportResult.(exportDoneMsg)
	if !ok {
		t.Fatalf("expected exportDoneMsg, got %T", exportResult)
	}
	if doneMsg.err != nil {
		t.Fatalf("export failed for project without book: %v", doneMsg.err)
	}
	if doneMsg.path == "" {
		t.Fatal("expected non-empty export path")
	}

	// Verify the file content.
	content, err := os.ReadFile(doneMsg.path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	// Should use the project name as the title.
	if !strings.Contains(string(content), "# NoBookTest") {
		t.Fatal("expected exported file to contain '# NoBookTest' (project name as title)")
	}
}

// TestHomeExportProjectNotSelected tests that pressing 'e' on the "Create a new book"
// row or the "Configure LLM" row does NOT start an export.
func TestHomeExportProjectNotSelected(t *testing.T) {
	ag := newTestAgent(t)
	_, err := ag.DB.CreateProject("TestProj")
	if err != nil {
		t.Fatal(err)
	}

	m := NewHomeModel(ag, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "TestProj"}}
	m.cursor = 0 // cursor on "Create a new book"

	// Press 'e' while cursor is on "Create a new book"
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	result, cmd := m.Update(keyMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd when pressing 'e' on 'Create a new book' row")
	}
	updated := result.(Model)
	if updated.homeExporting {
		t.Fatal("expected homeExporting to be false when 'e' pressed on 'Create a new book'")
	}

	// Also test cursor on "Configure LLM" (last item)
	m2 := NewHomeModel(ag, nil, true, "")
	m2.projects = []*db.Project{{ID: 1, Name: "TestProj"}}
	m2.cursor = 2 // cursor on "Configure LLM" (len(projects)+1 = 2)

	result, cmd = m2.Update(keyMsg)
	updated = result.(Model)
	if cmd != nil {
		t.Fatal("expected nil cmd when pressing 'e' on 'Configure LLM' row")
	}
	if updated.homeExporting {
		t.Fatal("expected homeExporting to be false when 'e' pressed on 'Configure LLM'")
	}
}

// ---------- Focused unit tests (no DB/file I/O) ----------

// TestHomePressEOnProjectStartsExport verifies that pressing 'e' when the cursor
// is on a project sets homeExporting=true and pre-fills the input with ".".
func TestHomePressEOnProjectStartsExport(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	result, cmd := m.Update(keyMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd (no cmd for key event), but got one")
	}
	updated := result.(Model)
	if !updated.homeExporting {
		t.Fatal("expected homeExporting to be true after pressing 'e' on a project")
	}
	if updated.homeExportResult != "" {
		t.Fatal("expected homeExportResult to start empty")
	}
	if updated.homeExportError != nil {
		t.Fatal("expected homeExportError to start nil")
	}
	if updated.input.Value() != "." {
		t.Fatalf("expected input to be pre-filled with '.', got %q", updated.input.Value())
	}
	if updated.project.ID != 1 {
		t.Fatalf("expected project.ID to be 1, got %d", updated.project.ID)
	}
}

// TestHomeExportEnterFiresExportCmd verifies that pressing Enter while in
// export mode returns a non-nil command (the exportHomeBook closure).
// With the two-step flow (path → subs choice → export), two Enter presses are needed.
func TestHomeExportEnterFiresExportCmd(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExporting = true
	m.homeExportStep = 1
	m.homeExportPath = "/tmp/test_export"
	m.input.SetValue("/tmp/test_export")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	// First Enter: save path, advance to step 2.
	result, cmd := m.Update(enterMsg)
	if cmd != nil {
		t.Fatal("expected nil cmd from first Enter (step 1: path → subs choice)")
	}

	m2 := result.(Model)
	if !m2.homeExporting {
		t.Fatal("expected homeExporting to still be true after first Enter (step 2)")
	}
	if m2.homeExportStep != 2 {
		t.Fatal("expected homeExportStep to be 2 after first Enter")
	}
	if m2.homeExportPath != "/tmp/test_export" {
		t.Fatalf("expected homeExportPath to be set from input, got %q", m2.homeExportPath)
	}

	// Second Enter: confirm subs choice, advance to format choice.
	result, cmd = m2.Update(enterMsg)
	if cmd != nil {
		t.Fatal("expected nil cmd from second Enter (step 2: subs → format choice)")
	}
	m3 := result.(Model)
	if !m3.homeExporting {
		t.Fatal("expected homeExporting to still be true after second Enter (step 3)")
	}
	if m3.homeExportStep != 3 {
		t.Fatal("expected homeExportStep to be 3 after second Enter")
	}

	// Third Enter: confirm format (default: markdown), fire export.
	result, cmd = m3.Update(enterMsg)
	if cmd == nil {
		t.Fatal("expected non-nil cmd (exportHomeBook closure) from third Enter")
	}
	updated := result.(Model)
	if updated.homeExporting {
		t.Fatal("expected homeExporting to be false after third Enter")
	}
	if updated.homeExportPath != "/tmp/test_export" {
		t.Fatalf("expected homeExportPath to remain set, got %q", updated.homeExportPath)
	}
}

// TestHomeExportEnterWithEmptyPathDefaultsToDot verifies that pressing Enter
// with an empty input defaults the export path to ".".
// With the two-step flow (path → subs choice → export), two Enter presses are needed.
func TestHomeExportEnterWithEmptyPathDefaultsToDot(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExporting = true
	m.homeExportStep = 1
	m.input.SetValue("") // empty input

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)
	_ = cmd
	m2 := result.(Model)

	// First Enter: path defaults to ".", advance to step 2.
	if m2.homeExportPath != "." {
		t.Fatalf("expected homeExportPath to default to '.', got %q", m2.homeExportPath)
	}
	if !m2.homeExporting {
		t.Fatal("expected homeExporting to still be true after first Enter (step 2)")
	}

	// Second Enter: confirm subs choice, advance to format choice.
	result, cmd = m2.Update(enterMsg)
	_ = cmd
	m3 := result.(Model)
	if !m3.homeExporting {
		t.Fatal("expected homeExporting to still be true after second Enter (step 3)")
	}

	// Third Enter: confirm format (default: markdown), fire export.
	result, cmd = m3.Update(enterMsg)
	updated := result.(Model)
	_ = cmd // cmd is the exportHomeBook closure (may fail without DB, that's fine)
	if updated.homeExportPath != "." {
		t.Fatalf("expected homeExportPath to still be '.', got %q", updated.homeExportPath)
	}
	if updated.homeExporting {
		t.Fatal("expected homeExporting to be false after third Enter")
	}
}

// TestHomeExportEscCancels verifies that pressing Esc while in export mode
// cancels the export and clears all export state fields.
func TestHomeExportEscCancels(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExporting = true
	m.homeExportResult = ""
	m.homeExportError = nil
	m.input.SetValue("/some/path")

	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	result, cmd := m.Update(escMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd from Esc cancel")
	}
	updated := result.(Model)
	if updated.homeExporting {
		t.Fatal("expected homeExporting to be false after Esc")
	}
	if updated.homeExportResult != "" {
		t.Fatal("expected homeExportResult to be cleared after Esc")
	}
	if updated.homeExportError != nil {
		t.Fatal("expected homeExportError to be nil after Esc")
	}
	if updated.input.Value() != "" {
		t.Fatal("expected input to be cleared after Esc")
	}
}

// TestHomeExportEnterOnResultOverlayClears verifies that pressing Enter on the
// export success/error overlay clears the result and returns to the project list.
func TestHomeExportEnterOnResultOverlayClears(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = "/tmp/test_export/MyBook/book.md" // simulate success
	m.homeExportError = nil

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd from Enter on result overlay")
	}
	updated := result.(Model)
	if updated.homeExportResult != "" {
		t.Fatal("expected homeExportResult to be cleared after Enter on result overlay")
	}
	if updated.homeExportError != nil {
		t.Fatal("expected homeExportError to be nil after Enter on result overlay")
	}
}

// TestHomeExportEnterOnErrorOverlayClears verifies that pressing Enter on an
// export error overlay also clears the result and returns to the project list.
func TestHomeExportEnterOnErrorOverlayClears(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = ""
	m.homeExportError = fmt.Errorf("write failed: permission denied")

	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	result, cmd := m.Update(enterMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd from Enter on error overlay")
	}
	updated := result.(Model)
	if updated.homeExportResult != "" {
		t.Fatal("expected homeExportResult to be cleared after Enter on error overlay")
	}
	if updated.homeExportError != nil {
		t.Fatal("expected homeExportError to be nil after Enter on error overlay")
	}
}

// TestHomeExportEPressedOnResultOverlayRetries verifies that pressing 'e' on
// the export result overlay clears the result and lets the navigation 'e'
// handler start a new export (cursor is still on the same project).
func TestHomeExportEPressedOnResultOverlayRetries(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = "/tmp/test_export/MyBook/book.md" // had a successful export

	// Press 'e' on the result overlay — should clear result and start new export.
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	result, cmd := m.Update(keyMsg)

	_ = cmd
	updated := result.(Model)
	if updated.homeExportResult != "" {
		t.Fatal("expected homeExportResult to be cleared after pressing 'e' on result")
	}
	// After clearing, the navigation 'e' handler should run, starting a new export.
	if !updated.homeExporting {
		t.Fatal("expected homeExporting to be true after pressing 'e' on result (new export)")
	}
}

// TestHomeExportEPressedOnErrorOverlayRetries verifies that pressing 'e' on the
// export error overlay clears the error and retries (starts a new export).
func TestHomeExportEPressedOnErrorOverlayRetries(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportError = fmt.Errorf("write failed: disk full") // had an error

	// Press 'e' on the error overlay — should clear error and retry.
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}
	result, cmd := m.Update(keyMsg)

	_ = cmd
	updated := result.(Model)
	if updated.homeExportError != nil {
		t.Fatal("expected homeExportError to be cleared after pressing 'e' on error")
	}
	if !updated.homeExporting {
		t.Fatal("expected homeExporting to be true after pressing 'e' on error (retry)")
	}
}

// TestHomeExportQOnResultOverlayQuits verifies that pressing 'q' on the
// export result or error overlay quits the app.
func TestHomeExportQOnResultOverlayQuits(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = "/tmp/exported.md"

	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	_, cmd := m.Update(keyMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected 'q' on export result overlay to quit")
	}

	// Also test 'q' on error overlay quits.
	m2 := NewHomeModel(nil, nil, true, "")
	m2.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m2.cursor = 1
	m2.homeExportError = fmt.Errorf("some error")

	_, cmd = m2.Update(keyMsg)
	if !isQuitCmd(cmd) {
		t.Fatal("expected 'q' on export error overlay to quit")
	}
}

// TestHomeExportCtrlCOnPathEntryQuits verifies that Ctrl+C during path entry
// quits the app (not just cancel the export).
func TestHomeExportCtrlCOnPathEntryQuits(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExporting = true

	ctrlCMsg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.Update(ctrlCMsg)

	if !isQuitCmd(cmd) {
		t.Fatal("expected Ctrl+C during export path entry to quit")
	}
}

// TestHomeExportRandomKeyOnResultOverlayIgnored verifies that random keys on
// the result overlay are ignored (no crash, no state change).
func TestHomeExportRandomKeyOnResultOverlayIgnored(t *testing.T) {
	m := NewHomeModel(nil, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = "/tmp/exported.md"

	// Press a random key (e.g., 'x') on the result overlay.
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	result, cmd := m.Update(keyMsg)

	if cmd != nil {
		t.Fatal("expected nil cmd for random key on result overlay")
	}
	updated := result.(Model)
	if updated.homeExportResult != "/tmp/exported.md" {
		t.Fatal("expected homeExportResult to remain unchanged after random key")
	}
}

// TestHomeExportViewShowsSuccessOverlay verifies the home view renders a
// success message when homeExportResult is set.
func TestHomeExportViewShowsSuccessOverlay(t *testing.T) {
	ag := newTestAgent(t)
	m := NewHomeModel(ag, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportResult = "/tmp/MyBook/book.md"
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "Exported successfully") {
		t.Fatal("expected export success view to show 'Exported successfully'")
	}
	if !strings.Contains(view, "/tmp/MyBook/book.md") {
		t.Fatal("expected export success view to show the export path")
	}
	if !strings.Contains(view, "[Enter] to go back") {
		t.Fatal("expected export success view to show '[Enter] to go back'")
	}
}

// TestHomeExportViewShowsErrorOverlay verifies the home view renders an
// error message when homeExportError is set.
func TestHomeExportViewShowsErrorOverlay(t *testing.T) {
	ag := newTestAgent(t)
	m := NewHomeModel(ag, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExportError = fmt.Errorf("write failed: disk full")
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "Export failed") {
		t.Fatal("expected export error view to show 'Export failed'")
	}
	if !strings.Contains(view, "disk full") {
		t.Fatal("expected export error view to show the error message")
	}
	if !strings.Contains(view, "[e] retry") {
		t.Fatal("expected export error view to show '[e] retry'")
	}
}

// TestHomeExportViewShowsPathInput verifies the home view renders the
// directory input prompt when homeExporting is true.
func TestHomeExportViewShowsPathInput(t *testing.T) {
	ag := newTestAgent(t)
	m := NewHomeModel(ag, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}}
	m.cursor = 1
	m.homeExporting = true
	m.input.SetValue("./output")
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "Export MyBook") {
		t.Fatal("expected path input view to show 'Export MyBook'")
	}
	if !strings.Contains(view, "[Enter] continue") {
		t.Fatal("expected path input view to show '[Enter] continue'")
	}
	if !strings.Contains(view, "[Esc] cancel") {
		t.Fatal("expected path input view to show '[Esc] cancel'")
	}
}

// TestHomeExportViewShowsExportHint verifies the home view shows an [e] export
// hint next to the selected project.
func TestHomeExportViewShowsExportHint(t *testing.T) {
	ag := newTestAgent(t)
	ag.DB.CreateProject("MyBook")
	ag.DB.CreateProject("OtherBook")

	m := NewHomeModel(ag, nil, true, "")
	m.projects = []*db.Project{{ID: 1, Name: "MyBook"}, {ID: 2, Name: "OtherBook"}}
	m.cursor = 2 // cursor on OtherBook
	m.width = 80
	m.height = 24

	view := m.View()
	// The selected project line should have the [e] export hint.
	if !strings.Contains(view, "OtherBook  —") {
		t.Fatal("expected view to show the selected project name")
	}
	if !strings.Contains(view, "[e] export") {
		t.Fatal("expected view to show '[e] export' hint on the selected project")
	}
	// The home screen footer should mention the 'e' key.
	if !strings.Contains(view, "[e]") {
		t.Fatal("expected view footer to mention [e] key binding")
	}
}
