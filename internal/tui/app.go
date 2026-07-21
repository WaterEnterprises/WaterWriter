package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/WaterEnterprises/WaterWriter/internal/agent"
	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/WaterEnterprises/WaterWriter/internal/llm"
	"github.com/WaterEnterprises/WaterWriter/internal/dict"
	"github.com/ZeroHawkeye/wordZero/pkg/document"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type state int

const (
	stateInit state = iota
	stateHome
	stateConfig
	stateQA
	stateThink
	stateQAReview
	stateWrite
	stateDone
	stateError
)

type streamMsg struct {
	chunk   string
	end     bool
	ci, si  int
	content string
	err     error
}

type qaReadyMsg struct {
	questions []string
	answers   []db.QAPair
	index     int
}

type qaSavedMsg struct {
	err error
}

type briefDoneMsg struct{}

type titleTOCDoneMsg struct{}

type subchaptersDoneMsg struct{}

type errMsg struct{ err error }

type configSavedMsg struct {
	client  *llm.Client
	ready   bool
	warning string
}

type exportDoneMsg struct {
	path string
	err  error
}

type modelsLoadedMsg struct {
	models []string
	err    error
}

type projectsLoadedMsg struct{ projects []*db.Project }

type phaseResolvedMsg struct{ phase string }

type chapterInfo struct {
	id    int
	title string
	subs  []subInfo
}

type subInfo struct {
	id      int
	title   string
	content string
	status  string
	written string // streamed display buffer
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	progressStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	// contentMaxWidth is the maximum width for centered content.
	contentMaxWidth = 80
)

type Model struct {
	agent   *agent.Agent
	project *db.Project
	ctx     context.Context
	cancel  context.CancelFunc
	width   int
	height  int
	state   state
	err     error

	// QA
	questions      []string
	qaIndex        int
	qaPairs        []db.QAPair
	qaReviewCursor int
	qaEditing      bool
	input          textinput.Model

	// Phase tracking
	phase string

	// Think / spinner
	spinner      spinner.Model
	thinkingText string

	// Writing
	chapters      []chapterInfo
	curChapterIdx int
	curSubIdx     int
	streamCh      chan streamMsg
	writeView     viewport.Model

	// Pending async saves count (for status indicator on review screen)
	qaPendingSaves int

	// QA char tracking (for paste detection timing window)
	qaLastChar time.Time

	// Export (from done screen)
	exportingPath bool
	exportPath    string
	exportResult  string
	exportError   error
	exportStep    int      // 0=off, 1=path entry, 2=subs choice

	// Export option: include subchapters in the TOC and body
	exportIncludeSubs bool

	// Export format: 0 = markdown, 1 = docx
	exportFormat int

	// Home-screen export (works for unfinished books)
	homeExporting  bool
	homeExportPath string
	homeExportResult string
	homeExportError  error
	homeExportStep int    // 0=off, 1=path entry, 2=subs choice, 3=format choice

	// Log
	logLines []string

	// Home screen
	projects   []*db.Project
	cursor     int
	creating   bool
	llmReady   bool
	llmWarning string

	// Config wizard
	configStep         int      // 0=provider, 1=api key, 2=model picker, 3=thinking effort, 4=saving
	configCursor       int      // cursor for provider list
	configProvs        []string // provider key list
	configSelProv      string   // selected provider key
	configAPIKey       string   // entered API key
	configModel        string   // entered model
	configModels       []string // fetched model list
	configModelCursor  int      // cursor for model list
	configLoading      bool     // loading models from API
	configLoadErr      string   // error from model loading
	configThinkingEff  int      // 0=default, 1=low, 2=medium, 3=high
	pendingAction      string   // "create" or "open" to resume after config

	// Overall counts
	totalSubs int
	doneSubs  int
}

func NewModel(ag *agent.Agent, proj *db.Project) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	s := spinner.NewModel()
	s.Spinner = spinner.Dot
	ti := textinput.NewModel()
	ti.Focus()
	ti.Placeholder = "Type your answer here..."
	ti.CharLimit = 0 // unlimited (paste can be very long)
	ti.Width = 72

	m := &Model{
		agent:     ag,
		project:   proj,
		ctx:       ctx,
		cancel:    cancel,
		state:     stateInit,
		input:     ti,
		spinner:   s,
		writeView: viewport.New(80, 20),
		phase:     "qa",
	}
	return m
}

// NewHomeModel creates a model that starts on the home screen (project picker).
// llmReady indicates the LLM client is configured (API key present).
// llmWarning is a message shown when the LLM is not configured.
func NewHomeModel(ag *agent.Agent, llmReady bool, llmWarning string) *Model {
	m := NewModel(ag, &db.Project{Name: "Water Writer"})
	m.state = stateHome
	m.projects = nil
	m.cursor = 0
	m.llmReady = llmReady
	m.llmWarning = llmWarning
	return m
}

func (m Model) Init() tea.Cmd {
	if m.state == stateHome {
		return tea.Batch(m.spinner.Tick, m.loadProjects())
	}
	return tea.Batch(
		m.spinner.Tick,
		m.resolvePhase(),
	)
}

func (m Model) loadProjects() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.agent.DB.ListProjects()
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg{projects: ps}
	}
}

func (m *Model) handleHomeKey(msg tea.KeyMsg) tea.Cmd {
	// While typing a new book name, let the text input handle all keys except
	// the few we intercept (create / cancel / quit).
	if m.creating {
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "enter":
			name := strings.TrimSpace(m.input.Value())
			if name == "" {
				return nil
			}
			proj, err := m.agent.DB.CreateProject(name)
			if err != nil {
				return func() tea.Msg { return errMsg{err} }
			}
			m.project = proj
			m.creating = false
			m.input.SetValue("")
			m.state = stateInit
			return m.resolvePhase()
		case "esc":
			m.creating = false
			m.input.SetValue("")
			return nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd
	}

	// While showing an export result or error overlay, handle key events.
	if m.homeExportResult != "" || m.homeExportError != nil {
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancel()
			return tea.Quit
		case "enter":
			// Go back to the project list
			m.homeExportResult = ""
			m.homeExportError = nil
			return nil
		case "e":
			// Retry or export another: clear result and fall through to navigation
			m.homeExportResult = ""
			m.homeExportError = nil
			// Don't return — fall through to the navigation switch below
		default:
			return nil
		}
	}

	// While export path entry or subs choice, handle Enter/Esc and pass other keys.
	if m.homeExporting {
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "enter":
			if m.homeExportStep == 1 {
				// Step 1: save path, ask about subchapters
				path := strings.TrimSpace(m.input.Value())
				if path == "" {
					path = "."
				}
				m.homeExportPath = path
				m.homeExportStep = 2
				m.input.SetValue("")
				m.input.Placeholder = "Y/n (default: Y)"
				return nil
			}
			if m.homeExportStep == 2 {
				// Step 2: save subs choice, ask about format
				choice := strings.TrimSpace(m.input.Value())
				m.exportIncludeSubs = choice == "" || strings.EqualFold(choice, "Y") || strings.EqualFold(choice, "yes")
				m.homeExportStep = 3
				m.input.SetValue("")
				m.input.Placeholder = "M/docx (default: M)"
				return nil
			}
			// Step 3: save format choice, run export
			fmtChoice := strings.TrimSpace(m.input.Value())
			m.exportFormat = 0
			if strings.EqualFold(fmtChoice, "W") || strings.EqualFold(fmtChoice, "docx") || strings.EqualFold(fmtChoice, "word") {
				m.exportFormat = 1
			}
			m.homeExporting = false
			m.homeExportStep = 0
			return m.exportHomeBook()
		case "esc":
			if m.homeExportStep == 3 {
				// Go back to subs choice
				m.homeExportStep = 2
				m.input.SetValue("")
				m.input.Placeholder = "Y/n (default: Y)"
				return nil
			}
			if m.homeExportStep == 2 {
				// Go back to path entry
				m.homeExportStep = 1
				m.input.SetValue(m.homeExportPath)
				m.input.Placeholder = "Export directory (default: .)"
				return nil
			}
			m.homeExporting = false
			m.homeExportStep = 0
			m.homeExportResult = ""
			m.homeExportError = nil
			m.input.SetValue("")
			return nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd
	}

	// Otherwise we're navigating the project list.
	switch msg.String() {
	case "ctrl+c", "q":
		m.cancel()
		return tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return nil
	case "down", "j":
		if m.cursor < len(m.projects)+1 {
			m.cursor++
		}
		return nil
	case "c":
		if !m.llmReady {
			m.startConfig("create")
			return nil
		}
		m.creating = true
		m.input.SetValue("")
		m.input.Placeholder = "Book name..."
		return nil
		case "o":
			m.startConfig("config")
			return nil
		case "e":
			// Export the currently selected project (works for unfinished books).
			if m.cursor > 0 && m.cursor-1 < len(m.projects) {
				m.project = m.projects[m.cursor-1]
				m.homeExporting = true
				m.homeExportStep = 1
				m.homeExportResult = ""
				m.homeExportError = nil
				m.input.SetValue(".")
				m.input.Placeholder = "Export directory (default: .)"
				return nil
			}
	case "enter":
		// First entry is "create a new book".
		if m.cursor == 0 {
			if !m.llmReady {
				m.startConfig("create")
				return nil
			}
			m.creating = true
			m.input.SetValue("")
			m.input.Placeholder = "Book name..."
			return nil
		}
		if m.cursor-1 < len(m.projects) {
			if !m.llmReady {
				m.startConfig("open")
				return nil
			}
			m.project = m.projects[m.cursor-1]
			m.state = stateInit
			return m.resolvePhase()
		}
		// Last entry is "Configure LLM".
		if m.cursor == len(m.projects)+1 {
			m.startConfig("config")
			return nil
		}
		return nil
	}
	return nil
}

func (m *Model) resolvePhase() tea.Cmd {
	return func() tea.Msg {
		phase := m.agent.DB.GetPhase(m.project.ID)
		m.agent.DB.UpdateProjectStatus(m.project.ID, phase)
		return phaseResolvedMsg{phase: phase}
	}
}

// startConfig enters the interactive LLM configuration wizard, preserving what
// the user was trying to do ("create" or "open") so we can resume after config.
func (m *Model) startConfig(pending string) {
	m.state = stateConfig
	m.configStep = 0
	m.configCursor = 0
	m.configProvs = llm.ListProviders()
	m.configSelProv = ""
	m.configAPIKey = ""
	m.configModel = ""
	m.configModels = nil
	m.configModelCursor = 0
	m.configLoading = false
	m.configLoadErr = ""
	m.configThinkingEff = 3 // default to High
	m.pendingAction = pending
	m.input.SetValue("")
	m.input.Placeholder = "Paste your API key here..."
}

// handleConfigKey processes keyboard input during the config wizard.
func (m *Model) handleConfigKey(msg tea.KeyMsg) tea.Cmd {
	switch m.configStep {
	case 0: // Provider selection
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancel()
			return tea.Quit
		case "esc":
			m.state = stateHome
			return nil
		case "up", "k":
			if m.configCursor > 0 {
				m.configCursor--
			}
			return nil
		case "down", "j":
			if m.configCursor < len(m.configProvs)-1 {
				m.configCursor++
			}
			return nil
		case "enter":
			m.configSelProv = m.configProvs[m.configCursor]
			preset := llm.Providers[m.configSelProv]
			m.configModel = preset.DefaultModel
			m.configStep = 1
			m.input.SetValue("")
			m.input.Placeholder = "API key (leave blank if not required)"
			if !preset.RequiresKey {
				// Skip API key step for providers that don't need one.
				m.configStep = 2
				m.configLoading = true
				m.configLoadErr = ""
				m.configModels = nil
				return m.configLoadModelsCmd()
			}
			return nil
		}
		return nil

	case 1: // API key entry
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "esc":
			m.configStep = 0
			return nil
		case "enter":
			m.configAPIKey = strings.TrimSpace(m.input.Value())
			m.configStep = 2
			m.configLoading = true
			m.configLoadErr = ""
			m.configModels = nil
			return m.configLoadModelsCmd()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return cmd
		}

	case 2: // Model picker (from API query)
		// If loading or error, don't process keyboard navigation
		if m.configLoading {
			return nil
		}
		if m.configLoadErr != "" {
			// Fall back to manual text entry on error
			switch msg.String() {
			case "ctrl+c":
				m.cancel()
				return tea.Quit
			case "esc":
				m.configStep = 1
				m.input.SetValue(m.configAPIKey)
				m.input.Placeholder = "API key (leave blank if not required)"
				return nil
			case "enter":
				entered := strings.TrimSpace(m.input.Value())
				if entered != "" {
					m.configModel = entered
				}
				m.configStep = 3
				return nil
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return cmd
			}
		}
		// Normal model list navigation
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "esc":
			m.configStep = 1
			m.input.SetValue(m.configAPIKey)
			m.input.Placeholder = "API key (leave blank if not required)"
			return nil
		case "up", "k":
			if m.configModelCursor > 0 {
				m.configModelCursor--
			}
			return nil
		case "down", "j":
			if m.configModelCursor < len(m.configModels)-1 {
				m.configModelCursor++
			}
			return nil
		case "enter":
			if m.configModelCursor >= 0 && m.configModelCursor < len(m.configModels) {
				m.configModel = m.configModels[m.configModelCursor]
			}
			m.configStep = 3
			return nil
		}
		return nil

	case 3: // Thinking effort picker
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "esc":
			m.configStep = 2
			return nil
		case "up", "k":
			if m.configThinkingEff > 0 {
				m.configThinkingEff--
			}
			return nil
		case "down", "j":
			if m.configThinkingEff < 3 {
				m.configThinkingEff++
			}
			return nil
		case "enter":
			// Don't set configStep = 4 here — wait for configSavedMsg to arrive
			// so the result view shows up-to-date readiness info.
			return m.saveConfig()
		}
		return nil

	case 4: // Saving (show result)
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return tea.Quit
		case "enter", "esc":
			if m.llmReady {
				// Execute the pending action
				switch m.pendingAction {
				case "create":
					m.state = stateHome
					m.creating = true
					m.input.SetValue("")
					m.input.Placeholder = "Book name..."
				case "open":
					m.state = stateHome
				default:
					m.state = stateHome
				}
			} else {
				m.state = stateHome
			}
			m.pendingAction = ""
			return nil
		}
		return nil
	}
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// The writing viewport fits within the centered content area (contentMaxWidth
		// minus 4 for left/right padding).
		m.writeView.Width = min(msg.Width-4, contentMaxWidth-4)
		m.writeView.Height = msg.Height - 8

	case tea.MouseMsg:
		if m.state == stateHome {
			if msg.Button == tea.MouseButtonLeft {
				// The home view is wrapped with 1 row of top padding, so the
				// content starts at terminal row 1. "Create a new book" is the
				// 6th content line; projects begin on the 8th content line.
				contentLine := msg.Y - 1
				if contentLine == 5 {
					m.creating = true
					m.input.SetValue("")
					m.input.Placeholder = "Book name..."
				} else if contentLine >= 7 && contentLine < 7+len(m.projects) {
					m.cursor = contentLine - 7 + 1
					m.project = m.projects[m.cursor-1]
					m.state = stateInit
					return m, m.resolvePhase()
				}
			}
			return m, nil
		}

	case tea.KeyMsg:
		if m.state == stateHome {
			return m, m.handleHomeKey(msg)
		}
		if m.state == stateConfig {
			return m, m.handleConfigKey(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "q":
			// Don't quit during QA text input — 'q' is a common letter in pasted text.
			// Users can still use Ctrl+C to quit from any state.
			if m.state != stateQA {
				m.cancel()
				return m, tea.Quit
			}
		case "e":
			if m.state == stateDone && !m.exportingPath {
				// Allow retry on error, but not after a successful export
				if m.exportResult == "" || m.exportError != nil {
					m.exportingPath = true
					m.exportStep = 1
					m.exportError = nil
					m.input.SetValue(".")
					m.input.Placeholder = "Export directory (default: .)"
					return m, nil
				}
			}
		case "esc":
			if m.state == stateDone && m.exportingPath {
				if m.exportStep == 3 {
					// Go back to subs choice
					m.exportStep = 2
					m.input.SetValue("")
					m.input.Placeholder = "Y/n (default: Y)"
					return m, nil
				}
				if m.exportStep == 2 {
					// Go back to path entry
					m.exportStep = 1
					m.input.SetValue(m.exportPath)
					m.input.Placeholder = "Export directory (default: .)"
					return m, nil
				}
				m.exportingPath = false
				m.input.SetValue("")
				return m, nil
			}
			if m.state == stateQA && m.qaEditing {
				// Cancel editing: return to review without saving
				m.qaEditing = false
				m.state = stateQAReview
				m.input.SetValue("")
				return m, nil
			}
		case "enter":
			if m.state == stateError {
				m.state = stateHome
				return m, nil
			}
			if m.state == stateQA {
				// Block Enter if it arrives within 50ms of the last character.
				// In unbracketed paste mode, newlines arrive as individual Enter
				// events within MICROSECONDS of the previous character. A deliberate
				// Enter press takes at least ~150ms (human reaction + finger movement).
				// The 50ms threshold catches paste-stream Enter events while letting
				// deliberate Enter (even after fast typing) through immediately.
				if time.Since(m.qaLastChar) < 50*time.Millisecond {
					return m, nil
				}
				answer := strings.TrimSpace(m.input.Value())
				if answer == "" {
					return m, nil
				}
				// Gracefully handle newlines: preserve paragraph structure
				// instead of collapsing everything to a single line.
				// First normalize all line endings to \n.
				answer = strings.ReplaceAll(answer, "\r\n", "\n")
				answer = strings.ReplaceAll(answer, "\r", "\n")
				// Reduce 3+ consecutive newlines to double (paragraph break).
				for strings.Contains(answer, "\n\n\n") {
					answer = strings.ReplaceAll(answer, "\n\n\n", "\n\n")
				}
				// Split by paragraphs, collapse within-paragraph newlines to spaces.
				paragraphs := strings.Split(answer, "\n\n")
				for i, p := range paragraphs {
					p = strings.Join(strings.Fields(p), " ")
					paragraphs[i] = strings.TrimSpace(p)
				}
				answer = strings.Join(paragraphs, "\n\n")
				m.qaLastChar = time.Now()

				question := m.questions[m.qaIndex]

				// Update in-memory state IMMEDIATELY (fast).
				if m.qaEditing {
					for i := range m.qaPairs {
						if m.qaPairs[i].Position == m.qaIndex+1 {
							m.qaPairs[i].Answer = answer
							break
						}
					}
				} else {
					m.qaPairs = append(m.qaPairs, db.QAPair{
						Question: question,
						Answer:   answer,
						Position: m.qaIndex + 1,
					})
				}

				// Fire async DB save (the bottleneck) so the UI doesn't block.
				// Capture all values the closure needs as local variables.
				m.qaPendingSaves++
				dbAgent := m.agent.DB
				projectID := m.project.ID
				position := m.qaIndex + 1
				isEditing := m.qaEditing
				q := question
				a := answer
				saveCmd := func() tea.Msg {
					var err error
					if isEditing {
						err = dbAgent.UpdateQAPair(projectID, position, a)
					} else {
						err = dbAgent.SaveQAPair(projectID, q, a, position)
					}
					if err != nil {
						return qaSavedMsg{err: err}
					}
					return qaSavedMsg{}
				}

				// Clear input and advance to next question immediately.
				m.input.SetValue("")
				m.qaIndex++
				if m.qaEditing {
					m.qaEditing = false
					m.state = stateQAReview
					return m, saveCmd
				}
				if m.qaIndex >= len(m.questions) {
					m.state = stateQAReview
					m.qaReviewCursor = 0
					return m, saveCmd
				}
				m.input.Placeholder = ""
				return m, saveCmd
			}
		if m.state == stateDone {
			if m.exportingPath {
				if m.exportStep == 1 {
					// Step 1: save path, ask about subchapters
					path := strings.TrimSpace(m.input.Value())
					if path == "" {
						path = "."
					}
					m.exportPath = path
					m.exportStep = 2
					m.input.SetValue("")
					m.input.Placeholder = "Y/n (default: Y)"
					return m, nil
				}
				if m.exportStep == 2 {
					// Step 2: save subs choice, ask about format
					choice := strings.TrimSpace(m.input.Value())
					m.exportIncludeSubs = choice == "" || strings.EqualFold(choice, "Y") || strings.EqualFold(choice, "yes")
					m.exportStep = 3
					m.input.SetValue("")
					m.input.Placeholder = "M/docx (default: M)"
					return m, nil
				}
				// Step 3: save format choice, run export
				fmtChoice := strings.TrimSpace(m.input.Value())
				m.exportFormat = 0
				if strings.EqualFold(fmtChoice, "W") || strings.EqualFold(fmtChoice, "docx") || strings.EqualFold(fmtChoice, "word") {
					m.exportFormat = 1
				}
				m.exportingPath = false
				m.exportStep = 0
				return m, m.exportBook()
			}
			return m, tea.Quit
		}
		}
		// QA review key handling
		if m.state == stateQAReview {
			switch msg.String() {
			case "ctrl+c", "q":
				m.cancel()
				return m, tea.Quit
			case "up", "k":
				if m.qaReviewCursor > 0 {
					m.qaReviewCursor--
				}
				return m, nil
			case "down", "j":
				if m.qaReviewCursor < len(m.questions) {
					m.qaReviewCursor++
				}
				return m, nil
			case "enter":
				if m.qaReviewCursor < len(m.questions) {
					// Edit this answer
					m.qaIndex = m.qaReviewCursor
					m.qaEditing = true
					m.state = stateQA
					// Pre-fill with existing answer if there is one
					for _, p := range m.qaPairs {
						if p.Position == m.qaReviewCursor+1 {
							m.input.SetValue(p.Answer)
							break
						}
					}
					m.input.Placeholder = "Edit your answer..."
					return m, nil
				}
				// Cursor is on the compile button
				m.state = stateThink
				m.thinkingText = "Compiling book brief..."
				return m, m.compileBrief()
			case "c":
				// Compile the brief
				m.state = stateThink
				m.thinkingText = "Compiling book brief..."
				return m, m.compileBrief()
			}
			return m, nil
		}

	// Projects loaded for the home screen
	case projectsLoadedMsg:
		m.projects = msg.projects
		// cursor can go to len(m.projects)+1 for the "Configure" menu item
		if m.cursor > len(m.projects)+1 {
			m.cursor = len(m.projects) + 1
		}
		return m, nil

	// Phase resolved
	case phaseResolvedMsg:
		m.phase = msg.phase
		switch msg.phase {
		case "qa":
			m.state = stateThink
			m.thinkingText = "Preparing interview questions..."
			return m, m.setupQA()
		case "brief":
			// Skip to brief compilation
			m.state = stateThink
			m.thinkingText = "Compiling book brief from prior Q&A..."
			return m, m.compileBrief()
		case "titletoc":
			m.state = stateThink
			m.thinkingText = "Generating title and table of contents..."
			return m, m.loadQAndGenerateTitleTOC()
		case "subchapters":
			m.state = stateThink
			m.thinkingText = "Generating subchapters for each chapter..."
			return m, m.generateAllSubchapters()
		case "writing":
			m.loadChapters()
			m.state = stateWrite
			return m, m.beginWrite()
		case "done":
			m.state = stateDone
			return m, nil
		}

	// Questions ready (resumes at first unanswered question)
	case qaReadyMsg:
		m.questions = msg.questions
		m.qaPairs = msg.answers
		m.qaIndex = msg.index
		m.qaLastChar = time.Time{}
		if m.qaIndex >= len(m.questions) {
			// All previously answered — show review screen
			m.state = stateQAReview
			m.qaReviewCursor = 0
			return m, nil
		}
		m.state = stateQA
		m.input.Placeholder = "Type your answer here..."
		return m, nil

	// Brief done
	case briefDoneMsg:
		m.log("Brief compiled successfully")
		m.state = stateThink
		m.thinkingText = "Generating title and table of contents..."
		return m, m.generateTitleTOC()

	// Title/TOC done
	case titleTOCDoneMsg:
		m.log("Title and table of contents generated")
		m.state = stateThink
		m.thinkingText = "Generating subchapters for each chapter..."
		return m, m.generateAllSubchapters()

	// Subchapters done
	case subchaptersDoneMsg:
		m.log("All subchapters generated")
		m.loadChapters()
		m.state = stateWrite
		return m, m.beginWrite()

	// Writing stream
	case streamMsg:
		if msg.end {
			if msg.err != nil {
				m.log(fmt.Sprintf("ERROR writing subchapter: %v", msg.err))
				return m, m.beginWrite() // retry next
			}
			ch := m.chapters[msg.ci]
			s := ch.subs[msg.si]
			// Fix LLM spacing issues before saving content.
		cleaned := normalizeSpacing(msg.content)
		m.agent.DB.UpdateSubchapterContent(s.id, cleaned)
			m.agent.DB.MarkTodoDoneByRef("subchapter", s.id)
			m.chapters[msg.ci].subs[msg.si].status = "done"
			m.chapters[msg.ci].subs[msg.si].content = cleaned
			m.doneSubs++
			m.log(fmt.Sprintf("✓ %s / %s", ch.title, s.title))
			// If the whole chapter is done, mark its todo + status
			chapterComplete := true
			for _, sub := range m.chapters[msg.ci].subs {
				if sub.status != "done" {
					chapterComplete = false
					break
				}
			}
			if chapterComplete {
				m.agent.DB.UpdateChapterStatus(m.chapters[msg.ci].id, "done")
				m.agent.DB.MarkTodoDoneByRef("chapter", m.chapters[msg.ci].id)
			}
			// Next subchapter
			if m.doneSubs >= m.totalSubs {
				m.state = stateDone
				m.exportingPath = false
				m.exportPath = ""
				m.exportResult = ""
				m.exportError = nil
				m.agent.DB.UpdateProjectStatus(m.project.ID, "done")
				return m, nil
			}
			return m, m.beginWrite()
		} else {
			// chunk
			if msg.ci >= 0 && msg.ci < len(m.chapters) && msg.si >= 0 && msg.si < len(m.chapters[msg.ci].subs) {
				m.chapters[msg.ci].subs[msg.si].written += msg.chunk
				m.writeView.SetContent(m.chapters[msg.ci].subs[msg.si].written)
				m.writeView.GotoBottom()
			}
			return m, m.listenStream()
		}

	// Config saved (result from background saveConfig command)
	case configSavedMsg:
		m.agent.LLM = msg.client
		m.llmReady = msg.ready
		m.llmWarning = msg.warning
		m.configStep = 4
		return m, nil

	// Models loaded from API query
	case modelsLoadedMsg:
		m.configLoading = false
		if msg.err != nil {
			m.configLoadErr = msg.err.Error()
			m.configModels = nil
			m.input.SetValue("")
			preset := llm.Providers[m.configSelProv]
			m.input.Placeholder = fmt.Sprintf("Model (default: %s) — query failed: %v", preset.DefaultModel, msg.err)
		} else {
			m.configModels = msg.models
			m.configModelCursor = 0
			m.configLoadErr = ""
		}
		return m, nil

	// QA pair saved asynchronously (silent — UI already updated)
	case qaSavedMsg:
		m.qaPendingSaves--
		if m.qaPendingSaves < 0 {
			m.qaPendingSaves = 0
		}
		if msg.err != nil {
			m.log(fmt.Sprintf("ERROR saving answer: %v", msg.err))
		}
		return m, nil

	// Error
	case errMsg:
		m.state = stateError
		m.err = msg.err
		return m, nil

	// Export done
	case exportDoneMsg:
		// Route result to the right set of fields based on current state.
		if m.state == stateHome {
			if msg.err != nil {
				m.homeExportError = msg.err
			} else {
				m.homeExportResult = msg.path
			}
		} else {
			if msg.err != nil {
				m.exportError = msg.err
			} else {
				m.exportResult = msg.path
			}
		}
		return m, nil

	// Spinner tick
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Handle input for states that use the text input widget
	if m.state == stateQA || (m.state == stateHome && m.creating) ||
		(m.state == stateHome && m.homeExporting) ||
		(m.state == stateConfig && (m.configStep == 1 || m.configStep == 2)) ||
		(m.state == stateDone && m.exportingPath) {
		// Track the time of the last key event for paste-Enter detection.
		// The Enter handler checks if an Enter arrives within 50ms of the
		// last character — if so, it's an unbracketed paste newline, not
		// a deliberate submission.
		if m.state == stateQA {
			if _, ok := msg.(tea.KeyMsg); ok {
				m.qaLastChar = time.Now()
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	return m, nil
}

// --- Commands ---

func (m Model) setupQA() tea.Cmd {
	return func() tea.Msg {
		questions, err := m.agent.DB.GetQAQuestions(m.project.ID)
		if err != nil || len(questions) == 0 {
			questions, err = m.agent.GenerateQuestions(m.ctx, 8)
			if err != nil {
				return errMsg{err}
			}
			if err := m.agent.DB.SaveQAQuestions(m.project.ID, questions); err != nil {
				return errMsg{err}
			}
		}
		pairs, err := m.agent.DB.GetQAPairs(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		index := len(pairs)
		if index > len(questions) {
			index = len(questions)
		}
		return qaReadyMsg{questions: questions, answers: pairs, index: index}
	}
}

func (m Model) compileBrief() tea.Cmd {
	return func() tea.Msg {
		pairs, err := m.agent.DB.GetQAPairs(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		brief, err := m.agent.CompileBrief(m.ctx, pairs)
		if err != nil {
			return errMsg{err}
		}
		if err := m.agent.DB.SaveBrief(m.project.ID, brief); err != nil {
			return errMsg{err}
		}
		m.agent.DB.UpdateProjectStatus(m.project.ID, "brief")
		return briefDoneMsg{}
	}
}

func (m Model) loadQAndGenerateTitleTOC() tea.Cmd {
	return func() tea.Msg {
		pairs, err := m.agent.DB.GetQAPairs(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		brief, err := m.agent.CompileBrief(m.ctx, pairs)
		if err != nil {
			// maybe brief already exists
			b, e := m.agent.DB.GetBrief(m.project.ID)
			if e != nil {
				return errMsg{err}
			}
			brief = b
		} else {
			m.agent.DB.SaveBrief(m.project.ID, brief)
		}
		resp, err := m.agent.GenerateTitleTOC(m.ctx, brief)
		if err != nil {
			return errMsg{err}
		}
		book, err := m.agent.DB.SaveBook(m.project.ID, resp.Title, resp.Subtitle)
		if err != nil {
			return errMsg{err}
		}
		// Delete existing chapters (and their subchapters) before re-inserting
		// to prevent duplicates when resuming from the "titletoc" phase.
		m.agent.DB.DeleteChaptersAndSubchapters(m.project.ID)
		m.agent.DB.DeleteTodos(m.project.ID)
		for i, ch := range resp.Chapters {
			saved, err := m.agent.DB.SaveChapter(book.ID, m.project.ID, i+1, ch)
			if err != nil {
				return errMsg{err}
			}
			m.agent.DB.SaveTodo(m.project.ID, "chapter", saved.ID, fmt.Sprintf("Write chapter: %s", ch))
		}
		m.agent.DB.UpdateProjectStatus(m.project.ID, "titletoc")
		return titleTOCDoneMsg{}
	}
}

func (m Model) generateTitleTOC() tea.Cmd {
	return func() tea.Msg {
		brief, err := m.agent.DB.GetBrief(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		resp, err := m.agent.GenerateTitleTOC(m.ctx, brief)
		if err != nil {
			return errMsg{err}
		}
		book, err := m.agent.DB.SaveBook(m.project.ID, resp.Title, resp.Subtitle)
		if err != nil {
			return errMsg{err}
		}
		for i, ch := range resp.Chapters {
			saved, err := m.agent.DB.SaveChapter(book.ID, m.project.ID, i+1, ch)
			if err != nil {
				return errMsg{err}
			}
			m.agent.DB.SaveTodo(m.project.ID, "chapter", saved.ID, fmt.Sprintf("Write chapter: %s", ch))
		}
		m.agent.DB.UpdateProjectStatus(m.project.ID, "titletoc")
		return titleTOCDoneMsg{}
	}
}

func (m Model) generateAllSubchapters() tea.Cmd {
	return func() tea.Msg {
		brief, err := m.agent.DB.GetBrief(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		book, err := m.agent.DB.GetBook(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		chapters, err := m.agent.DB.GetChapters(m.project.ID)
		if err != nil {
			return errMsg{err}
		}
		for _, ch := range chapters {
			// Delete existing subchapters for this chapter before regenerating,
			// preventing duplicates on retry or partial generation.
			// Delete todos FIRST while the subchapters still exist in the DB,
			// so the IN subquery finds the rows to clean up.
			m.agent.DB.Exec(`DELETE FROM todos WHERE project_id = ? AND kind = 'subchapter' AND ref_id IN (SELECT id FROM subchapters WHERE chapter_id = ?)`, m.project.ID, ch.ID)
			m.agent.DB.Exec(`DELETE FROM subchapters WHERE chapter_id = ?`, ch.ID)

			subs, err := m.agent.GenerateSubchapters(m.ctx, brief, ch.Title)
			if err != nil {
				return errMsg{fmt.Errorf("generate subchapters for %q: %w", ch.Title, err)}
			}
			for j, subTitle := range subs {
				s, err := m.agent.DB.SaveSubchapter(ch.ID, book.ID, m.project.ID, j+1, subTitle)
				if err != nil {
					return errMsg{err}
				}
				m.agent.DB.SaveTodo(m.project.ID, "subchapter", s.ID, fmt.Sprintf("Write %s / %s", ch.Title, subTitle))
			}
		}
		m.agent.DB.UpdateProjectStatus(m.project.ID, "subchapters")
		return subchaptersDoneMsg{}
	}
}

func (m *Model) loadChapters() {
	chapters, err := m.agent.DB.GetChapters(m.project.ID)
	if err != nil {
		return
	}
	m.chapters = nil
	m.totalSubs = 0
	m.doneSubs = 0
	for _, ch := range chapters {
		ci := chapterInfo{id: ch.ID, title: ch.Title}
		subs, _ := m.agent.DB.GetSubchapters(ch.ID)
		for _, s := range subs {
			ci.subs = append(ci.subs, subInfo{
				id:      s.ID,
				title:   s.Title,
				content: s.Content,
				status:  s.Status,
			})
			m.totalSubs++
			if s.Status == "done" {
				m.doneSubs++
			}
		}
		m.chapters = append(m.chapters, ci)
	}
}

func (m Model) findNextPending() (int, int) {
	for ci, ch := range m.chapters {
		for si, s := range ch.subs {
			if s.status != "done" && s.status == "pending" {
				return ci, si
			}
			// also catch empty status from new subchapters
			if s.status == "" {
				return ci, si
			}
		}
	}
	return -1, -1
}

func (m *Model) beginWrite() tea.Cmd {
	ci, si := m.findNextPending()
	if ci < 0 {
		m.state = stateDone
		return nil
	}
	m.curChapterIdx = ci
	m.curSubIdx = si
	m.chapters[ci].subs[si].written = ""

	// Create stream channel and start goroutine
	ch := make(chan streamMsg, 200)
	m.streamCh = ch

	go func() {
		chapter := m.chapters[ci]
		sub := chapter.subs[si]
		dbChapter := &db.Chapter{ID: chapter.id, Title: chapter.title}
		dbSub := &db.Subchapter{ID: sub.id, Title: sub.title}

		prompt, err := m.agent.BuildWriteContext(m.ctx, m.project.ID, dbChapter, dbSub)
		if err != nil {
			ch <- streamMsg{end: true, ci: ci, si: si, err: err}
			close(ch)
			return
		}

		content, err := m.agent.WriteSubchapterStream(m.ctx, prompt, func(chunk string) error {
			ch <- streamMsg{chunk: chunk, ci: ci, si: si}
			return nil
		})
		ch <- streamMsg{end: true, ci: ci, si: si, content: content, err: err}
		close(ch)
	}()

	return m.listenStream()
}

func (m Model) listenStream() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.streamCh
		if !ok {
			return nil
		}
		return msg
	}
}

// exportHomeBook builds the full book markdown for the current project and writes it.
// Works for any project, even unfinished ones — exports whatever content exists.
func (m *Model) exportHomeBook() tea.Cmd {
	return func() tea.Msg {
		projectID := m.project.ID
		book, err := m.agent.DB.GetBook(projectID)
		if err != nil {
			// No book yet — export what little we have (title from project name).
			chapters, _ := m.agent.DB.GetChapters(projectID)
			return m.writeExportFile(m.homeExportPath, m.project.Name, "", chapters)
		}
		chapters, err := m.agent.DB.GetChapters(projectID)
		if err != nil {
			return exportDoneMsg{err: fmt.Errorf("get chapters: %w", err)}
		}
		return m.writeExportFile(m.homeExportPath, book.Title, book.Subtitle, chapters)
	}
}

// writeExportFile is shared by both the done-screen and home-screen export flows.
func (m *Model) writeExportFile(outDir, title, subtitle string, chapters []*db.Chapter) tea.Msg {
	dir := filepath.Join(outDir, sanitize(title))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exportDoneMsg{err: fmt.Errorf("create directory: %w", err)}
	}

	var fullBook strings.Builder
	fullBook.WriteString(fmt.Sprintf("# %s\n\n", title))
	if subtitle != "" {
		fullBook.WriteString(fmt.Sprintf("## %s\n\n", subtitle))
	}

	// Generate Table of Contents between title and body
	fullBook.WriteString("## Table of Contents\n\n")
	for i, ch := range chapters {
		fullBook.WriteString(fmt.Sprintf("- [Chapter %d: %s](#chapter-%d-%s)\n",
			i+1, ch.Title, i+1, tocAnchor(ch.Title)))
		if m.exportIncludeSubs {
			subs, _ := m.agent.DB.GetSubchapters(ch.ID)
			for j, s := range subs {
				if s.Content != "" {
					fullBook.WriteString(fmt.Sprintf("  - [%d.%d %s](#%s)\n",
						i+1, j+1, s.Title, tocAnchor(s.Title)))
				}
			}
		}
	}
	fullBook.WriteString("\n---\n\n")

	for i, ch := range chapters {
		fullBook.WriteString(fmt.Sprintf("\n## Chapter %d: %s\n\n", i+1, ch.Title))
		subs, err := m.agent.DB.GetSubchapters(ch.ID)
		if err != nil {
			return exportDoneMsg{err: fmt.Errorf("get subchapters for %q: %w", ch.Title, err)}
		}
		for _, s := range subs {
			if s.Content != "" {
				// Strip any leading #-style markdown headings from LLM content.
				// The LLM often writes headings like "#### Subchapter Title" at
				// the start of its output, creating a "duplicate subchapter" look.
				// The exporter already adds the correct ### heading, so we strip
				// any that the LLM included. Only LEADING headings are removed.
				content := strings.TrimLeft(s.Content, "\n\r\t ")
				for strings.HasPrefix(content, "#") {
					idx := strings.IndexByte(content, '\n')
					if idx < 0 {
						content = ""
						break
					}
					content = strings.TrimLeft(content[idx+1:], "\n\r\t ")
				}
				if content == "" {
					continue
				}
				// Fix LLM spacing issues (merged words) at export time safe-net.
				content = normalizeSpacing(content)
				if m.exportIncludeSubs {
					fullBook.WriteString(fmt.Sprintf("### %s\n\n", s.Title))
				}
				fullBook.WriteString(content)
				fullBook.WriteString("\n\n")
			}
		}
	}

	// Always write the markdown file.
	bookPath := filepath.Join(dir, "book.md")
	if err := os.WriteFile(bookPath, []byte(fullBook.String()), 0o644); err != nil {
		return exportDoneMsg{err: fmt.Errorf("write file: %w", err)}
	}

	// Optionally create DOCX directly from structured data.
	if m.exportFormat == 1 {
		if err := m.writeDocxFile(dir, title, subtitle, chapters); err != nil {
			return exportDoneMsg{err: fmt.Errorf("create docx: %w", err)}
		}
	}

	return exportDoneMsg{path: bookPath}
}

// writeDocxFile creates a Word document directly from structured book data,
// bypassing markdown conversion entirely to avoid formatting bugs.
func (m *Model) writeDocxFile(outDir, title, subtitle string, chapters []*db.Chapter) error {
	doc := document.New()

	// Title (heading level 1)
	doc.AddHeadingParagraph(title, 1)

	// Subtitle (heading level 2)
	if subtitle != "" {
		doc.AddHeadingParagraph(subtitle, 2)
	}

	// Table of Contents heading
	doc.AddHeadingParagraph("Table of Contents", 2)
	for i, ch := range chapters {
		doc.AddParagraph(fmt.Sprintf("Chapter %d: %s", i+1, ch.Title))
		if m.exportIncludeSubs {
			subs, _ := m.agent.DB.GetSubchapters(ch.ID)
			for _, s := range subs {
				doc.AddParagraph(fmt.Sprintf("  %d.%d %s", i+1, s.Position, s.Title))
			}
		}
	}

	// Chapter content
	for i, ch := range chapters {
		doc.AddHeadingParagraph(fmt.Sprintf("Chapter %d: %s", i+1, ch.Title), 2)

		if m.exportIncludeSubs {
			subs, err := m.agent.DB.GetSubchapters(ch.ID)
			if err != nil {
				return fmt.Errorf("get subchapters for %q: %w", ch.Title, err)
			}
			for _, s := range subs {
				if s.Content == "" {
					continue
				}
				content := cleanContent(s.Content)
				if content == "" {
					continue
				}
				// Fix LLM spacing issues (merged words) at export time.
				content = normalizeSpacing(content)
				doc.AddHeadingParagraph(s.Title, 3)
				for _, p := range strings.Split(content, "\n\n") {
					p = strings.TrimSpace(p)
					if p != "" {
						// Collapse single newlines to spaces (markdown behavior).
						// The LLM often outputs single \n within paragraphs;
						// markdown renders these as spaces, so we do the same.
						doc.AddParagraph(strings.Join(strings.Fields(p), " "))
					}
				}
			}
		}
	}

	docxPath := filepath.Join(outDir, "book.docx")
	return doc.Save(docxPath)
}

// exportBook builds the full book markdown and writes it to the export path.
func (m *Model) exportBook() tea.Cmd {
	return func() tea.Msg {
		book, err := m.agent.DB.GetBook(m.project.ID)
		if err != nil {
			return exportDoneMsg{err: fmt.Errorf("get book: %w", err)}
		}
		chapters, err := m.agent.DB.GetChapters(m.project.ID)
		if err != nil {
			return exportDoneMsg{err: fmt.Errorf("get chapters: %w", err)}
		}
		return m.writeExportFile(m.exportPath, book.Title, book.Subtitle, chapters)
	}
}

// cleanContent strips leading #-style markdown headings from text content.
// This is reused by both markdown export and DOCX export.
func cleanContent(content string) string {
	c := strings.TrimLeft(content, "\n\r\t ")
	for strings.HasPrefix(c, "#") {
		idx := strings.IndexByte(c, '\n')
		if idx < 0 {
			return ""
		}
		c = strings.TrimLeft(c[idx+1:], "\n\r\t ")
	}
	return c
}

// sanitize makes a string safe for use as a directory name.
func sanitize(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return r.Replace(s)
}

// centeredView wraps content in a centered layout using the terminal dimensions.
func (m Model) centeredView(content string) string {
	if m.width == 0 || m.height == 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(content)
	}
	w := contentMaxWidth
	if m.width-4 < w {
		w = m.width - 4
	}
	styled := lipgloss.NewStyle().
		Width(w).
		Align(lipgloss.Left).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, styled)
}

// --- View ---

// normalizeSpacing post-processes LLM content to fix common spacing issues.
// LLMs occasionally merge adjacent words or omit spaces after punctuation
// due to tokenization quirks. This function corrects those patterns.
func normalizeSpacing(s string) string {
	// Fix missing space after period followed by capital letter (new sentence).
	s = regexp.MustCompile(`\.([A-Z])`).ReplaceAllString(s, ". $1")
	// Fix missing space after comma.
	s = regexp.MustCompile(`,([a-zA-Z])`).ReplaceAllString(s, ", $1")
	// Fix missing space after ? or !.
	s = regexp.MustCompile(`([?!])([a-zA-Z])`).ReplaceAllString(s, "$1 $2")

	// Fix "wordof" at end of word before space/punct (e.g., "blanketof " →
	// "blanket of "). Matches "of" only when followed by whitespace,
	// punctuation, or end-of-string. This avoids false positives like
	// "coffee" → "c of fee" (where 'e' follows "of", not a space).
	s = regexp.MustCompile(`([a-z])of([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 of$2")

	// Fix "wordwas" at end of word before space/punct (e.g., "mirrorwas " →
	// "mirror was "). No English word has "was" as an interior substring.
	s = regexp.MustCompile(`([a-zA-Z])was([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 was$2")
	// Fix " wasword" at start of word (e.g., " wasfinalized" → " was finalized").
	s = regexp.MustCompile(`([\s.,;!?\-])was([a-zA-Z])`).ReplaceAllString(s, "${1}was $2")

	// Fix "wordthat" at end of word before space/punct (e.g.,
	// "somethingthat " → "something that "). Low false-positive risk.
	s = regexp.MustCompile(`([a-z])that([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 that$2")
	// Fix " thatword" at start of word (e.g., " thatpulsed" → " that pulsed").
	s = regexp.MustCompile(`([\s.,;!?\-])that([a-zA-Z])`).ReplaceAllString(s, "${1}that $2")

	// Fix article "a" at end of word before space/punct (e.g.,
	// "experiencinga " → "experiencing a ").
	s = regexp.MustCompile(`([a-zA-Z])a([\s.,;!?\-]|$)`).ReplaceAllString(s, "$1 a$2")

	// Dictionary-based word segmentation for remaining merged words.
	// This catches patterns that regex alone can't safely handle, like
	// "enteredthrough" → "entered through", "hadbeen" → "had been", etc.
	s = dict.SplitUnknown(s)

	return s
}

func (m Model) View() string {
	switch m.state {
	case stateInit:
		return m.loadingView("Initializing...")

	case stateHome:
		return m.homeView()

	case stateConfig:
		return m.configView()

	case stateQA:
		return m.qaView()

	case stateQAReview:
		return m.qaReviewView()

	case stateThink:
		return m.thinkingView()

	case stateWrite:
		return m.writingView()

	case stateDone:
		return m.doneView()

	case stateError:
		return m.errorView()

	default:
		return "Unknown state"
	}
}

func (m Model) loadingView(text string) string {
	return m.centeredView(fmt.Sprintf("\n  %s %s\n", m.spinner.View(), text))
}

func (m Model) configView() string {
	var b strings.Builder

	switch m.configStep {
	case 0: // Provider selection
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		b.WriteString(" Select your LLM provider:\n\n")

		for i, key := range m.configProvs {
			preset := llm.Providers[key]
			prefix := "  "
			if m.configCursor == i {
				prefix = "> "
			}
			keyReq := ""
			if preset.RequiresKey {
				keyReq = infoStyle.Render(" [key required]")
			} else {
				keyReq = successStyle.Render(" [no key needed]")
			}
			b.WriteString(fmt.Sprintf("%s%-12s %s%s\n", prefix, key, preset.Name, keyReq))
		}

		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  [↑/↓] move   •   [Enter] select   •   [Esc] back   •   [q] quit"))

	case 1: // API key entry
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf(" Provider: %s\n\n", infoStyle.Render(m.configSelProv)))
		b.WriteString(" Enter your API key:\n\n")
		b.WriteString(fmt.Sprintf("  %s\n", m.input.View()))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  [Enter] continue   •   [Esc] change provider   •   [Ctrl+C] quit"))

	case 2: // Model picker (query API → interactive list)
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf(" Provider: %s\n\n", infoStyle.Render(m.configSelProv)))

		if m.configLoading {
			b.WriteString(fmt.Sprintf("  %s Querying available models...\n", m.spinner.View()))
		} else if m.configLoadErr != "" {
			// Fallback: manual text entry
			b.WriteString(errorStyle.Render("  ⚠️ Could not fetch models"))
			b.WriteString("\n")
			b.WriteString(dimStyle.Render(fmt.Sprintf("     %s", m.configLoadErr)))
			b.WriteString("\n\n")
			b.WriteString(" Enter model name manually (or leave blank for default):\n\n")
			b.WriteString(fmt.Sprintf("  %s\n", m.input.View()))
			preset := llm.Providers[m.configSelProv]
			b.WriteString("\n")
			b.WriteString(dimStyle.Render(fmt.Sprintf("  Default: %s", preset.DefaultModel)))
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  [Enter] continue   •   [Esc] change API key   •   [Ctrl+C] quit"))
		} else {
			b.WriteString(" Select a model:\n\n")
			const maxShow = 50
			shown := m.configModels
			if len(shown) > maxShow {
				shown = shown[:maxShow]
			}
			for i, model := range shown {
				prefix := "  "
				if m.configModelCursor == i {
					prefix = "> "
				}
				b.WriteString(fmt.Sprintf("%s%s\n", prefix, model))
			}
			if len(m.configModels) > maxShow {
				b.WriteString(fmt.Sprintf("  ... (%d more not shown)\n", len(m.configModels)-maxShow))
			}
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  [↑/↓] move   •   [Enter] select   •   [Esc] back   •   [Ctrl+C] quit"))
		}

	case 3: // Thinking effort picker
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf(" Provider: %s\n", infoStyle.Render(m.configSelProv)))
		if m.configModel != "" {
			b.WriteString(fmt.Sprintf(" Model:    %s\n\n", infoStyle.Render(m.configModel)))
		}
		b.WriteString(" Thinking effort:\n\n")

		teOptions := []struct {
			val  int
			name string
			desc string
		}{
			{0, "Default", "No special reasoning effort"},
			{1, "Low",     "Minimum reasoning (faster, fewer tokens)"},
			{2, "Medium",  "Moderate reasoning"},
			{3, "High",    "Maximum reasoning (best quality, more tokens)"},
		}
		for _, opt := range teOptions {
			prefix := "  "
			if m.configThinkingEff == opt.val {
				prefix = "> "
			}
			b.WriteString(fmt.Sprintf("%s%-8s %s\n", prefix, opt.name, dimStyle.Render(opt.desc)))
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  [↑/↓] move   •   [Enter] save   •   [Esc] back   •   [Ctrl+C] quit"))

	case 4: // Save result
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		if m.llmReady {
			b.WriteString(successStyle.Render(" ✓ Configuration saved successfully!"))
			b.WriteString("\n\n")
			b.WriteString(fmt.Sprintf(" Provider: %s\n", infoStyle.Render(m.configSelProv)))
			if m.configModel != "" {
				b.WriteString(fmt.Sprintf(" Model:    %s\n", infoStyle.Render(m.configModel)))
			}
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  API key saved to database"))
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  [Enter] to continue"))
		} else {
			b.WriteString(errorStyle.Render(" ✗ Configuration incomplete"))
			b.WriteString("\n\n")
			b.WriteString(fmt.Sprintf("  %s\n", m.llmWarning))
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  [Enter] to go back"))
		}
	}

	return m.centeredView(b.String())
}

func (m Model) homeView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer"))

	// LLM status / warning banner
	if m.llmReady && m.agent.LLM != nil {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("   %s • %s", m.agent.LLM.Provider, m.agent.LLM.Model)))
	} else if !m.llmReady {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(" ⚠️  LLM not configured"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("    %s", m.llmWarning)))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    Press [c] or [Enter] to configure via the setup wizard"))
	}

	// Show export result (success or error) in place of the project list
	if m.homeExportResult != "" {
		b.WriteString("\n\n")
		b.WriteString(successStyle.Render(" ✓ Exported successfully!"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s\n\n", m.homeExportResult))
		b.WriteString(dimStyle.Render("  [Enter] to go back   •   [e] export another   •   [q] quit"))
		return m.centeredView(b.String())
	}

	if m.homeExportError != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(" ✗ Export failed"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %v\n\n", m.homeExportError))
		b.WriteString(dimStyle.Render("  [Enter] to go back   •   [e] retry   •   [q] quit"))
		return m.centeredView(b.String())
	}

	if m.homeExporting {
		b.WriteString("\n\n")
		projName := ""
		if m.cursor > 0 && m.cursor-1 < len(m.projects) {
			projName = m.projects[m.cursor-1].Name
		}
		b.WriteString(fmt.Sprintf(" Export %s\n\n", infoStyle.Render(projName)))
		if m.homeExportStep == 2 || m.homeExportStep == 3 {
			b.WriteString(fmt.Sprintf(" Directory: %s\n\n", infoStyle.Render(m.homeExportPath)))
		}
		if m.homeExportStep == 3 {
			subsLabel := "Yes"
			if !m.exportIncludeSubs {
				subsLabel = "No"
			}
			b.WriteString(fmt.Sprintf(" Subchapter sections: %s\n\n", infoStyle.Render(subsLabel)))
			b.WriteString(" Format:\n\n")
		}
		if m.homeExportStep == 2 {
			b.WriteString(" Include subchapter sections?\n\n")
		}
		b.WriteString(fmt.Sprintf("  %s\n", m.input.View()))
		b.WriteString("\n")
		if m.homeExportStep == 2 || m.homeExportStep == 3 {
			b.WriteString(dimStyle.Render("  [Enter] confirm   •   [Esc] back"))
		} else {
			b.WriteString(dimStyle.Render("  [Enter] continue   •   [Esc] cancel"))
		}
		return m.centeredView(b.String())
	}

	b.WriteString("\n\n")
	b.WriteString(" Select a project or create a new one:\n\n")

	prefix := "  "
	if m.cursor == 0 {
		prefix = "> "
	}
	b.WriteString(fmt.Sprintf("%s➕  Create a new book\n", prefix))
	b.WriteString("\n")

	if len(m.projects) == 0 {
		b.WriteString(dimStyle.Render("  (no projects yet)\n"))
	}
	for i, p := range m.projects {
		prefix := "  "
		if m.cursor == i+1 {
			prefix = "> "
		}
		phase := m.agent.DB.GetPhase(p.ID)
		exportHint := ""
		if m.cursor == i+1 {
			exportHint = dimStyle.Render("  [e] export")
		}
		b.WriteString(fmt.Sprintf("%s%s  —  %s%s\n", prefix, p.Name, phase, exportHint))
	}

	// Configure menu item
	configIdx := len(m.projects) + 1
	confPrefix := "  "
	if m.cursor == configIdx {
		confPrefix = "> "
	}
	b.WriteString(fmt.Sprintf("%s⚙️  Configure LLM\n", confPrefix))

	b.WriteString("\n")
	if m.creating {
		b.WriteString(fmt.Sprintf("  New book name: %s\n", m.input.View()))
		b.WriteString(dimStyle.Render("  [Enter] create   •   [Esc] cancel"))
	} else {
		b.WriteString(dimStyle.Render("  [↑/↓] move   •   [Enter] select   •   [e] export   •   [c] new   •   [o] config   •   [q] quit"))
	}
	return m.centeredView(b.String())
}

func (m Model) qaReviewView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer — Review Answers"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf(" %s\n\n", infoStyle.Render(m.project.Name)))

	for i, q := range m.questions {
		// Find the answer for this question
		answer := ""
		for _, p := range m.qaPairs {
			if p.Position == i+1 {
				answer = p.Answer
				break
			}
		}

		prefix := "  "
		if m.qaReviewCursor == i {
			prefix = "> "
		}

		qLabel := infoStyle.Render(fmt.Sprintf("Q%d:", i+1))
		qText := q
		if len(qText) > 50 {
			qText = qText[:47] + "..."
		}
		aText := dimStyle.Render("(no answer)")
		if answer != "" {
			aText = answer
			if len(aText) > 60 {
				aText = aText[:57] + "..."
			}
		}

		b.WriteString(fmt.Sprintf("%s%s %s\n", prefix, qLabel, qText))
		b.WriteString(fmt.Sprintf("  %s\n\n", aText))
	}

	// Show pending save indicator when saves are still in-flight
	if m.qaPendingSaves > 0 {
		b.WriteString(fmt.Sprintf("  %s %s\n\n", m.spinner.View(), dimStyle.Render("Saving...")))
	}

	// "Compile" button at the bottom
	if m.qaReviewCursor >= len(m.questions) {
		b.WriteString("> ")
	} else {
		b.WriteString("  ")
	}
	b.WriteString(successStyle.Render("✓ Confirm and compile brief"))

	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  [↑/↓] navigate   •   [Enter] edit / confirm   •   [c] compile   •   [q] quit"))

	return m.centeredView(b.String())
}

func (m Model) qaView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer — Interview"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf(" %s (%d/%d)\n\n", infoStyle.Render(m.project.Name), m.qaIndex+1, len(m.questions)))

	if m.qaIndex < len(m.questions) {
		b.WriteString(fmt.Sprintf(" %s\n\n", m.questions[m.qaIndex]))
		b.WriteString(fmt.Sprintf(" %s\n", m.input.View()))
		b.WriteString(dimStyle.Render(" [Enter] to submit  •  [Ctrl+C] to quit"))
	} else {
		b.WriteString(" All questions answered! Compiling...")
	}

	return m.centeredView(b.String())
}

func (m Model) thinkingView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", m.spinner.View(), m.thinkingText))
	if len(m.logLines) > 0 {
		b.WriteString("\n")
		for _, l := range m.logLines[len(m.logLines)-5:] {
			b.WriteString(fmt.Sprintf("  %s\n", dimStyle.Render(l)))
		}
	}
	b.WriteString(fmt.Sprintf("\n  %s", dimStyle.Render("[Ctrl+C] to quit")))
	return m.centeredView(b.String())
}

func (m Model) writingView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer — Writing Mode"))
	b.WriteString("\n")

	// Progress bar
	pct := 0.0
	if m.totalSubs > 0 {
		pct = float64(m.doneSubs) / float64(m.totalSubs) * 100
	}
	bar := progressBar(int(pct), min(50, m.width-20))
	b.WriteString(fmt.Sprintf("  %s  %s %d/%d\n\n", bar, progressStyle.Render(fmt.Sprintf("%.0f%%", pct)), m.doneSubs, m.totalSubs))

	// Current position
	if m.curChapterIdx >= 0 && m.curChapterIdx < len(m.chapters) {
		ch := m.chapters[m.curChapterIdx]
		b.WriteString(infoStyle.Render(fmt.Sprintf("  Chapter %d: %s", m.curChapterIdx+1, ch.title)))
		if m.curSubIdx >= 0 && m.curSubIdx < len(ch.subs) {
			b.WriteString(fmt.Sprintf("  → %s\n\n", ch.subs[m.curSubIdx].title))
		}
	}

	// Streamed content
	b.WriteString(m.writeView.View())

	b.WriteString(fmt.Sprintf("\n\n  %s", dimStyle.Render("[Ctrl+C] to quit")))

	return m.centeredView(b.String())
}

func (m Model) doneView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📖 Water Writer — Complete!"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n\n", successStyle.Render("✓"), infoStyle.Render("Project:")))

	book, err := m.agent.DB.GetBook(m.project.ID)
	if err == nil {
		b.WriteString(fmt.Sprintf("  Title: %s\n", book.Title))
		if book.Subtitle != "" {
			b.WriteString(fmt.Sprintf("  Subtitle: %s\n", book.Subtitle))
		}
	}

	b.WriteString(fmt.Sprintf("  %d subchapters written\n", m.doneSubs))
	b.WriteString("\n")
	b.WriteString(successStyle.Render("  🎉 Your book is complete!"))
	b.WriteString("\n\n")

	if m.exportingPath {
		b.WriteString(fmt.Sprintf(" Export to:\n\n"))
		if m.exportStep == 2 || m.exportStep == 3 {
			b.WriteString(fmt.Sprintf("  Directory: %s\n\n", infoStyle.Render(m.exportPath)))
		}
		if m.exportStep == 3 {
			subsLabel := "Yes"
			if !m.exportIncludeSubs {
				subsLabel = "No"
			}
			b.WriteString(fmt.Sprintf(" Subchapter sections: %s\n\n", infoStyle.Render(subsLabel)))
			b.WriteString(" Format:\n\n")
		}
		if m.exportStep == 2 {
			b.WriteString(" Include subchapter sections?\n\n")
		}
		b.WriteString(fmt.Sprintf("  %s\n", m.input.View()))
		b.WriteString("\n")
		if m.exportStep == 2 || m.exportStep == 3 {
			b.WriteString(dimStyle.Render("  [Enter] confirm   •   [Esc] back"))
		} else {
			b.WriteString(dimStyle.Render("  [Enter] continue   •   [Esc] cancel"))
		}
	} else if m.exportResult != "" {
		b.WriteString(successStyle.Render(" ✓ Exported successfully!"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s\n\n", m.exportResult))
		b.WriteString(dimStyle.Render("  [Enter] to exit"))
	} else if m.exportError != nil {
		b.WriteString(errorStyle.Render(" ✗ Export failed"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %v\n\n", m.exportError))
		b.WriteString(dimStyle.Render("  [Enter] to exit   •   [e] retry"))
	} else {
		b.WriteString(dimStyle.Render("  [e] Export"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  [Enter] to exit"))
	}

	return m.centeredView(b.String())
}

// configLoadModelsCmd queries the provider for available models.
func (m *Model) configLoadModelsCmd() tea.Cmd {
	provider := m.configSelProv
	apiKey := m.configAPIKey
	preset := llm.Providers[provider]

	return func() tea.Msg {
		tmpClient := llm.NewClientFromConfig(llm.Config{
			Provider: provider,
			APIKey:   apiKey,
			Style:    string(preset.Style),
			BaseURL:  preset.BaseURL,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		models, err := tmpClient.ListModels(ctx)
		if err != nil {
			return modelsLoadedMsg{models: nil, err: err}
		}
		// Sort models alphabetically for easier browsing.
		// (ListModels returns them in provider order which may be arbitrary.)
		out := make([]string, len(models))
		copy(out, models)
		sort.Strings(out)
		return modelsLoadedMsg{models: out, err: nil}
	}
}

// saveConfig persists the wizard's choices and re-initializes the LLM client.
// It returns a configSavedMsg that should be handled in Update to apply state
// changes thread-safely (Bubble Tea runs command closures in goroutines).
func (m *Model) saveConfig() tea.Cmd {
	// Snapshot config values inside the closure (the model pointer captured here
	// is stable for reading since this is called from Update).
	provider := m.configSelProv
	modelVal := m.configModel
	apiKey := m.configAPIKey
	thinkingEff := ""
	switch m.configThinkingEff {
	case 1:
		thinkingEff = "low"
	case 2:
		thinkingEff = "medium"
	case 3:
		thinkingEff = "high"
	}

	return func() tea.Msg {
		// Save provider, model, API key, and thinking effort to the database settings.
		m.agent.DB.SetSettings(map[string]string{
			db.SettingProvider:       provider,
			db.SettingModel:          modelVal,
			db.SettingAPIKey:         apiKey,
			db.SettingThinkingEffort: thinkingEff,
		})

		// Re-create the LLM client with the new configuration including thinking effort.
		preset := llm.Providers[provider]
		client := llm.NewClientFromConfig(llm.Config{
			Provider:       provider,
			Model:          modelVal,
			APIKey:         apiKey,
			Style:          string(preset.Style),
			BaseURL:        preset.BaseURL,
			ThinkingEffort: thinkingEff,
		})

		// Re-check readiness (no model mutation here).
		ready := true
		warning := ""
		if ok, msg := client.Ready(); !ok {
			ready = false
			warning = msg
		}

		return configSavedMsg{client: client, ready: ready, warning: warning}
	}
}

func (m Model) errorView() string {
	var b strings.Builder
	b.WriteString(errorStyle.Render("✗ Error"))
	b.WriteString("\n\n")
	if m.err != nil {
		b.WriteString(fmt.Sprintf("  %v\n", m.err))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  [Enter] to go back  •  [Ctrl+C] to quit"))
	return m.centeredView(b.String())
}

func (m *Model) log(text string) {
	m.logLines = append(m.logLines, text)
}

func progressBar(pct int, width int) string {
	if width <= 0 {
		return ""
	}
	filled := pct * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return progressStyle.Render(bar)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// tocAnchor converts a heading into a GitHub-style markdown anchor ID.
// Lowercases, replaces spaces with hyphens, removes non-alphanumeric chars.
func tocAnchor(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == ' ' {
			if r == ' ' {
				b.WriteByte('-')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
