package db

import (
	"database/sql"
	"fmt"
)

// --- Settings (global key/value store, e.g. selected LLM provider/model) ---

func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetSettings returns the values for the given keys (empty string if unset).
func (d *DB) GetSettings(keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := d.GetSetting(k)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// SetSettings stores several key/value pairs in one call.
func (d *DB) SetSettings(values map[string]string) error {
	for k, v := range values {
		if err := d.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

// LLM setting keys.
const (
	SettingProvider       = "provider"
	SettingModel          = "model"
	SettingBaseURL        = "base_url"
	SettingStyle          = "style"
	SettingThinkingEffort = "thinking_effort"
	SettingAPIKey         = "api_key"
)

func (d *DB) CreateProject(name string) (*Project, error) {
	ts := now()
	res, err := d.Exec(`INSERT INTO projects (name, status, created_at, updated_at) VALUES (?, 'created', ?, ?)`, name, ts, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Project{ID: int(id), Name: name, Status: "created", CreatedAt: ts, UpdatedAt: ts}, nil
}

func (d *DB) GetProject(name string) (*Project, error) {
	p := &Project{}
	err := d.QueryRow(`SELECT id, name, status, created_at, updated_at FROM projects WHERE name = ?`, name).Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (d *DB) ListProjects() ([]*Project, error) {
	rows, err := d.Query(`SELECT id, name, status, created_at, updated_at FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) UpdateProjectStatus(id int, status string) error {
	_, err := d.Exec(`UPDATE projects SET status = ?, updated_at = ? WHERE id = ?`, status, now(), id)
	return err
}

func (d *DB) SaveQAPairs(projectID int, pairs []QAPair) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	for _, p := range pairs {
		_, err := tx.Exec(`INSERT INTO qa_pairs (project_id, question, answer, position, created_at) VALUES (?, ?, ?, ?, ?)`,
			projectID, p.Question, p.Answer, p.Position, ts)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) SaveQAQuestions(projectID int, questions []string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	for i, q := range questions {
		if _, err := tx.Exec(`INSERT INTO qa_questions (project_id, position, question, created_at) VALUES (?, ?, ?, ?)`,
			projectID, i+1, q, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) GetQAQuestions(projectID int) ([]string, error) {
	rows, err := d.Query(`SELECT question FROM qa_questions WHERE project_id = ? ORDER BY position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (d *DB) SaveQAPair(projectID int, question, answer string, position int) error {
	_, err := d.Exec(`INSERT INTO qa_pairs (project_id, question, answer, position, created_at) VALUES (?, ?, ?, ?, ?)`,
		projectID, question, answer, position, now())
	return err
}

func (d *DB) DeleteQAQuestions(projectID int) error {
	_, err := d.Exec(`DELETE FROM qa_questions WHERE project_id = ?`, projectID)
	return err
}

func (d *DB) GetQAPairs(projectID int) ([]QAPair, error) {
	rows, err := d.Query(`SELECT id, project_id, question, answer, position, created_at FROM qa_pairs WHERE project_id = ? ORDER BY position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QAPair
	for rows.Next() {
		var p QAPair
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Question, &p.Answer, &p.Position, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) DeleteQAPairs(projectID int) error {
	_, err := d.Exec(`DELETE FROM qa_pairs WHERE project_id = ?`, projectID)
	return err
}

func (d *DB) UpdateQAPair(projectID int, position int, answer string) error {
	_, err := d.Exec(`UPDATE qa_pairs SET answer = ?, created_at = ? WHERE project_id = ? AND position = ?`,
		answer, now(), projectID, position)
	return err
}

func (d *DB) SaveBrief(projectID int, content string) error {
	_, err := d.Exec(`INSERT OR REPLACE INTO briefs (project_id, content, created_at) VALUES (?, ?, ?)`,
		projectID, content, now())
	return err
}

func (d *DB) GetBrief(projectID int) (string, error) {
	var content string
	err := d.QueryRow(`SELECT content FROM briefs WHERE project_id = ?`, projectID).Scan(&content)
	if err != nil {
		return "", err
	}
	return content, nil
}

func (d *DB) SaveBook(projectID int, title, subtitle string) (*Book, error) {
	ts := now()
	res, err := d.Exec(`INSERT OR REPLACE INTO books (project_id, title, subtitle, created_at) VALUES (?, ?, ?, ?)`,
		projectID, title, subtitle, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// OR REPLACE, get existing
		book, _ := d.GetBook(projectID)
		if book != nil {
			return book, nil
		}
	}
	return &Book{ID: int(id), ProjectID: projectID, Title: title, Subtitle: subtitle, CreatedAt: ts}, nil
}

func (d *DB) GetBook(projectID int) (*Book, error) {
	b := &Book{}
	err := d.QueryRow(`SELECT id, project_id, title, subtitle, created_at FROM books WHERE project_id = ?`, projectID).Scan(&b.ID, &b.ProjectID, &b.Title, &b.Subtitle, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (d *DB) SaveChapter(bookID, projectID, position int, title string) (*Chapter, error) {
	ts := now()
	res, err := d.Exec(`INSERT INTO chapters (book_id, project_id, position, title, status, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		bookID, projectID, position, title, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Chapter{ID: int(id), BookID: bookID, ProjectID: projectID, Position: position, Title: title, Status: "pending", CreatedAt: ts}, nil
}

func (d *DB) GetChapters(projectID int) ([]*Chapter, error) {
	rows, err := d.Query(`SELECT id, book_id, project_id, position, title, summary, status, created_at FROM chapters WHERE project_id = ? ORDER BY position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.ID, &c.BookID, &c.ProjectID, &c.Position, &c.Title, &c.Summary, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (d *DB) UpdateChapterStatus(id int, status string) error {
	_, err := d.Exec(`UPDATE chapters SET status = ? WHERE id = ?`, status, now(), id)
	return err
}

func (d *DB) SaveSubchapter(chapterID, bookID, projectID, position int, title string) (*Subchapter, error) {
	ts := now()
	res, err := d.Exec(`INSERT INTO subchapters (chapter_id, book_id, project_id, position, title, status, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		chapterID, bookID, projectID, position, title, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Subchapter{ID: int(id), ChapterID: chapterID, BookID: bookID, ProjectID: projectID, Position: position, Title: title, Status: "pending", CreatedAt: ts}, nil
}

func (d *DB) GetSubchapters(chapterID int) ([]*Subchapter, error) {
	rows, err := d.Query(`SELECT id, chapter_id, book_id, project_id, position, title, status, content, created_at FROM subchapters WHERE chapter_id = ? ORDER BY position`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subchapter
	for rows.Next() {
		var s Subchapter
		if err := rows.Scan(&s.ID, &s.ChapterID, &s.BookID, &s.ProjectID, &s.Position, &s.Title, &s.Status, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (d *DB) GetAllSubchapters(projectID int) ([]*Subchapter, error) {
	rows, err := d.Query(`SELECT id, chapter_id, book_id, project_id, position, title, status, content, created_at FROM subchapters WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subchapter
	for rows.Next() {
		var s Subchapter
		if err := rows.Scan(&s.ID, &s.ChapterID, &s.BookID, &s.ProjectID, &s.Position, &s.Title, &s.Status, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (d *DB) UpdateSubchapterContent(id int, content string) error {
	_, err := d.Exec(`UPDATE subchapters SET content = ?, status = 'done' WHERE id = ?`, content, id)
	return err
}

func (d *DB) DeleteChaptersAndSubchapters(projectID int) error {
	d.Exec(`DELETE FROM subchapters WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM chapters WHERE project_id = ?`, projectID)
	return nil
}

func (d *DB) SaveTodo(projectID int, kind string, refID int, text string) (*Todo, error) {
	ts := now()
	res, err := d.Exec(`INSERT INTO todos (project_id, kind, ref_id, text, done, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		projectID, kind, refID, text, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Todo{ID: int(id), ProjectID: projectID, Kind: kind, RefID: refID, Text: text, CreatedAt: ts}, nil
}

func (d *DB) GetTodos(projectID int) ([]*Todo, error) {
	rows, err := d.Query(`SELECT id, project_id, kind, ref_id, text, done, created_at FROM todos WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Todo
	for rows.Next() {
		var t Todo
		var done int
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Kind, &t.RefID, &t.Text, &done, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Done = done != 0
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (d *DB) MarkTodoDone(id int) error {
	_, err := d.Exec(`UPDATE todos SET done = 1 WHERE id = ?`, id)
	return err
}

func (d *DB) MarkTodoDoneByRef(kind string, refID int) error {
	_, err := d.Exec(`UPDATE todos SET done = 1 WHERE kind = ? AND ref_id = ?`, kind, refID)
	return err
}

func (d *DB) DeleteTodos(projectID int) error {
	_, err := d.Exec(`DELETE FROM todos WHERE project_id = ?`, projectID)
	return err
}

func (d *DB) GetPhase(projectID int) string {
	countQA := 0
	d.QueryRow(`SELECT COUNT(*) FROM qa_pairs WHERE project_id = ?`, projectID).Scan(&countQA)
	countQuestions := 0
	d.QueryRow(`SELECT COUNT(*) FROM qa_questions WHERE project_id = ?`, projectID).Scan(&countQuestions)

	// Stay in the Q&A phase until every question has an answer.
	if countQuestions > 0 {
		if countQA < countQuestions {
			return "qa"
		}
	} else if countQA == 0 {
		return "qa"
	}

	countBrief := 0
	d.QueryRow(`SELECT COUNT(*) FROM briefs WHERE project_id = ?`, projectID).Scan(&countBrief)
	if countBrief == 0 {
		return "brief"
	}
	countBooks := 0
	d.QueryRow(`SELECT COUNT(*) FROM books WHERE project_id = ?`, projectID).Scan(&countBooks)
	if countBooks == 0 {
		return "titletoc"
	}
	countSubchapters := 0
	d.QueryRow(`SELECT COUNT(*) FROM subchapters WHERE project_id = ?`, projectID).Scan(&countSubchapters)
	if countSubchapters == 0 {
		return "subchapters"
	}
	countPending := 0
	d.QueryRow(`SELECT COUNT(*) FROM subchapters WHERE project_id = ? AND status = 'pending'`, projectID).Scan(&countPending)
	if countPending > 0 {
		return "writing"
	}
	return "done"
}

// --- Translations ---

// DeleteTranslation removes a translation record by ID.
func (d *DB) DeleteTranslation(id int) error {
	_, err := d.Exec(`DELETE FROM translations WHERE id = ?`, id)
	return err
}

// DeletePendingTranslations removes all incomplete translations for a project
// and language combination. Used when starting a new translation to prevent
// duplicate entries accumulating from failed or interrupted runs.
func (d *DB) DeletePendingTranslations(projectID int, language string) error {
	_, err := d.Exec(`DELETE FROM translations WHERE project_id = ? AND language = ? AND status != 'complete'`, projectID, language)
	return err
}

func (d *DB) SaveTranslation(projectID int, language string) (*Translation, error) {
	ts := now()
	res, err := d.Exec(`INSERT INTO translations (project_id, language, status, file_path, total_subs, done_subs, created_at, updated_at) VALUES (?, ?, 'in_progress', '', 0, 0, ?, ?)`,
		projectID, language, ts, ts)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Translation{ID: int(id), ProjectID: projectID, Language: language, Status: "in_progress", CreatedAt: ts, UpdatedAt: ts}, nil
}

func (d *DB) GetTranslations(projectID int) ([]*Translation, error) {
	rows, err := d.Query(`SELECT id, project_id, language, status, file_path, total_subs, done_subs, created_at, updated_at FROM translations WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Translation
	for rows.Next() {
		t := &Translation{}
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Language, &t.Status, &t.FilePath, &t.TotalSubs, &t.DoneSubs, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) UpdateTranslation(id int, status, filePath string, totalSubs, doneSubs int) error {
	_, err := d.Exec(`UPDATE translations SET status = ?, file_path = ?, total_subs = ?, done_subs = ?, updated_at = ? WHERE id = ?`,
		status, filePath, totalSubs, doneSubs, now(), id)
	return err
}

func (d *DB) DeleteProjectData(projectID int) error {
	d.Exec(`DELETE FROM translations WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM todos WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM subchapters WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM chapters WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM books WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM briefs WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM qa_pairs WHERE project_id = ?`, projectID)
	d.Exec(`DELETE FROM qa_questions WHERE project_id = ?`, projectID)
	return nil
}

func (d *DB) DeleteProject(name string) error {
	p, err := d.GetProject(name)
	if err != nil {
		return fmt.Errorf("project %q not found", name)
	}
	d.DeleteProjectData(p.ID)
	_, err = d.Exec(`DELETE FROM projects WHERE id = ?`, p.ID)
	return err
}
