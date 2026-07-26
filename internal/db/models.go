package db

type Project struct {
	ID        int
	Name      string
	Status    string
	CreatedAt string
	UpdatedAt string
}

type QAPair struct {
	ID        int
	ProjectID int
	Question  string
	Answer    string
	Position  int
	CreatedAt string
}

type Brief struct {
	ID        int
	ProjectID int
	Content   string
	CreatedAt string
}

type Book struct {
	ID        int
	ProjectID int
	Title     string
	Subtitle  string
	CreatedAt string
}

type Chapter struct {
	ID        int
	BookID    int
	ProjectID int
	Position  int
	Title     string
	Summary   string
	Status    string
	CreatedAt string
}

type Subchapter struct {
	ID         int
	ChapterID  int
	BookID     int
	ProjectID  int
	Position   int
	Title      string
	Status     string
	Content    string
	CreatedAt  string
}

type Todo struct {
	ID        int
	ProjectID int
	Kind      string
	RefID     int
	Text      string
	Done      bool
	CreatedAt string
}

type Translation struct {
	ID        int
	ProjectID int
	Language  string
	Status    string // "in_progress" or "complete"
	FilePath  string
	TotalSubs int
	DoneSubs  int
	CreatedAt string
	UpdatedAt string
}
