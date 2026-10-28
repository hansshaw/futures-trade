// Package journal provides versioned private notes, immutable screenshot assets,
// and an auditable review assistant. AI output never mutates the original notes.
package journal

import "time"

type Journal struct {
	ID            string    `json:"id"`
	Date          string    `json:"date"`
	Title         string    `json:"title"`
	Symbol        string    `json:"symbol"`
	Tags          []string  `json:"tags"`
	Plan          string    `json:"plan"`
	Thesis        string    `json:"thesis"`
	Execution     string    `json:"execution"`
	Review        string    `json:"review"`
	Lessons       string    `json:"lessons"`
	Emotion       string    `json:"emotion"`
	Discipline    int       `json:"discipline"`
	PNL           *float64  `json:"pnl"`
	AttachmentIDs []string  `json:"attachment_ids"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Revision      uint64    `json:"revision"`
	Archived      bool      `json:"archived"`
}

type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int       `json:"size"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`
	DataURL   string    `json:"data_url,omitempty"`
}

type Pattern struct {
	Title       string   `json:"title"`
	Observation string   `json:"observation"`
	JournalIDs  []string `json:"journal_ids"`
}

type Lesson struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	EvidenceIDs []string  `json:"evidence_ids"`
	Status      string    `json:"status"`
	ReviewID    string    `json:"review_id,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

type Review struct {
	ID               string            `json:"id"`
	CreatedAt        time.Time         `json:"created_at"`
	Model            string            `json:"model"`
	JournalIDs       []string          `json:"journal_ids"`
	InputRevisions   map[string]uint64 `json:"input_revisions"`
	Summary          string            `json:"summary"`
	Patterns         []Pattern         `json:"patterns"`
	Actions          []string          `json:"actions"`
	Questions        []string          `json:"questions"`
	RiskFlags        []string          `json:"risk_flags"`
	ImageCount       int               `json:"image_count"`
	Lessons          []Lesson          `json:"lessons"`
	InputSHA256      string            `json:"input_sha256"`
	ContextLessonIDs []string          `json:"context_lesson_ids"`
}

type ReviewRequest struct {
	JournalIDs         []string `json:"journal_ids"`
	UseLLM             bool     `json:"use_llm"`
	IncludeImages      bool     `json:"include_images"`
	UseAcceptedLessons *bool    `json:"use_accepted_lessons,omitempty"`
}

// Snapshot persists the exact input independently of editable journals. Accepted
// lessons keep their original review IDs so contextual advice is traceable.
type Snapshot struct {
	Journals        []Journal         `json:"journals"`
	AcceptedLessons []Lesson          `json:"accepted_lessons"`
	IncludeImages   bool              `json:"include_images"`
	ImageSHA256     map[string]string `json:"image_sha256"`
}
