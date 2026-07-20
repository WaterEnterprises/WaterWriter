package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/WaterEnterprises/WaterWriter/internal/agent"
	"github.com/WaterEnterprises/WaterWriter/internal/db"
	"github.com/WaterEnterprises/WaterWriter/internal/llm"
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

type briefDoneMsg struct{}

type titleTOCDoneMsg struct{}

type subchaptersDoneMsg struct{}

type errMsg struct{ err error }

type configSavedMsg struct {
	client  *llm.Client
	ready   bool
	warning string
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
	questions []string
	qaIndex   int
	qaPairs   []db.QAPair
	input     textinput.Model

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

	// Log
	logLines []string

	// Home screen
	projects   []*db.Project
	cursor     int
	creating   bool
	llmReady   bool
	llmWarning string

	// Config wizard
	configStep    int      // 0=provider, 1=api key, 2=model, 3=saving
	configCursor  int      // cursor for provider list
	configProvs   []string // provider key list
	configSelProv string   // selected provider key
	configAPIKey  string   // entered API key
	configModel   string   // entered model
	pendingAction string   // "create" or "open" to resume after config

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
	ti.CharLimit = 1000
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
		if m.cursor < len(m.projects) {
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
				m.input.SetValue("")
				m.input.Placeholder = fmt.Sprintf("Model (default: %s)", preset.DefaultModel)
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
			preset := llm.Providers[m.configSelProv]
			m.input.SetValue("")
			m.input.Placeholder = fmt.Sprintf("Model (default: %s)", preset.DefaultModel)
			return nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return cmd
		}

	case 2: // Model entry (optional)
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
			// Don't set configStep = 3 here — wait for configSavedMsg to arrive
			// so the result view shows up-to-date readiness info.
			return m.saveConfig()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return cmd
		}

	case 3: // Saving (show result)
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
		case "ctrl+c", "q":
			m.cancel()
			return m, tea.Quit
		case "enter":
			if m.state == stateError {
				return m, tea.Quit
			}
			if m.state == stateQA {
				answer := strings.TrimSpace(m.input.Value())
				if answer == "" {
					return m, nil
				}
				question := m.questions[m.qaIndex]
				if err := m.agent.DB.SaveQAPair(m.project.ID, question, answer, m.qaIndex+1); err != nil {
					return m, func() tea.Msg { return errMsg{err} }
				}
				m.qaPairs = append(m.qaPairs, db.QAPair{
					Question: question,
					Answer:   answer,
					Position: m.qaIndex + 1,
				})
				m.input.SetValue("")
				m.qaIndex++
				if m.qaIndex >= len(m.questions) {
					m.state = stateThink
					m.thinkingText = "Compiling book brief..."
					return m, m.compileBrief()
				}
				m.input.Placeholder = ""
				return m, nil
			}
			if m.state == stateDone {
				return m, tea.Quit
			}
		}

	// Projects loaded for the home screen
	case projectsLoadedMsg:
		m.projects = msg.projects
		if m.cursor > len(m.projects) {
			m.cursor = len(m.projects)
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
		if m.qaIndex >= len(m.questions) {
			m.state = stateThink
			m.thinkingText = "Compiling book brief..."
			return m, m.compileBrief()
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
			m.agent.DB.UpdateSubchapterContent(s.id, msg.content)
			m.agent.DB.MarkTodoDoneByRef("subchapter", s.id)
			m.chapters[msg.ci].subs[msg.si].status = "done"
			m.chapters[msg.ci].subs[msg.si].content = msg.content
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
		m.configStep = 3
		return m, nil

	// Error
	case errMsg:
		m.state = stateError
		m.err = msg.err
		return m, nil

	// Spinner tick
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Handle input
	if m.state == stateQA || (m.state == stateHome && m.creating) ||
		(m.state == stateConfig && (m.configStep == 1 || m.configStep == 2)) {
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

	case 2: // Model entry
		preset := llm.Providers[m.configSelProv]
		b.WriteString(titleStyle.Render("⚙️ Water Writer — Setup"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf(" Provider: %s\n\n", infoStyle.Render(m.configSelProv)))
		b.WriteString(" Enter the model name (or leave blank for default):\n\n")
		b.WriteString(fmt.Sprintf("  %s\n", m.input.View()))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Default: %s", preset.DefaultModel)))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  [Enter] save   •   [Esc] change API key   •   [Ctrl+C] quit"))

	case 3: // Save result
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
			b.WriteString(dimStyle.Render(fmt.Sprintf("  API key saved to: %s", filepath.Join(".", ".env"))))
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

	// LLM warning banner
	if !m.llmReady {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(" ⚠️  LLM not configured"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("    %s", m.llmWarning)))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    Create a .env file with WATERWRITER_LLM_API_KEY or run: waterwriter config"))
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
		b.WriteString(fmt.Sprintf("%s%s  —  %s\n", prefix, p.Name, phase))
	}

	b.WriteString("\n")
	if m.creating {
		b.WriteString(fmt.Sprintf("  New book name: %s\n", m.input.View()))
		b.WriteString(dimStyle.Render("  [Enter] create   •   [Esc] cancel"))
	} else {
		b.WriteString(dimStyle.Render("  [↑/↓] move   •   [Enter] open   •   [c] new   •   [q] quit"))
	}
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
	b.WriteString(dimStyle.Render("  [Enter] to exit"))

	return m.centeredView(b.String())
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

	return func() tea.Msg {
		// Save provider and model to the database settings.
		m.agent.DB.SetSettings(map[string]string{
			db.SettingProvider: provider,
			db.SettingModel:    modelVal,
		})

		// Write the API key to .env in the current directory.
		if apiKey != "" {
			envPath := filepath.Join(".", ".env")
			envContent := ""
			if data, err := os.ReadFile(envPath); err == nil {
				envContent = string(data)
			}
			lines := strings.Split(envContent, "\n")
			found := false
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "WATERWRITER_LLM_API_KEY=") {
					lines[i] = fmt.Sprintf("WATERWRITER_LLM_API_KEY=%s", apiKey)
					found = true
					break
				}
			}
			if found {
				envContent = strings.Join(lines, "\n")
			} else {
				if envContent != "" && !strings.HasSuffix(envContent, "\n") {
					envContent += "\n"
				}
				envContent += fmt.Sprintf("WATERWRITER_LLM_API_KEY=%s\n", apiKey)
			}
			_ = os.WriteFile(envPath, []byte(envContent), 0644)
		}

		// Re-create the LLM client with the new configuration.
		preset := llm.Providers[provider]
		client := llm.NewClientFromConfig(llm.Config{
			Provider: provider,
			Model:    modelVal,
			APIKey:   apiKey,
			Style:    string(preset.Style),
			BaseURL:  preset.BaseURL,
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
	b.WriteString(dimStyle.Render("  [Enter] to quit"))
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
