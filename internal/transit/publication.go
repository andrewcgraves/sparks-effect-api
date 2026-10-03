package transit

import "time"

type ServicePublication struct {
	UserServiceID string    `json:"user_service_id"`
	CompileJobID  string    `json:"compile_job_id"`
	Name          string    `json:"name"`
	Subtext       string    `json:"subtext,omitempty"`
	Description   string    `json:"description,omitempty"`
	Routes        []Route   `json:"routes"`
	PublishedAt   time.Time `json:"published_at"`
	// AuthorName is the owner's current display name, read live rather than
	// frozen with the snapshot, so a rename or a transfer reaches it without a
	// republish. Never the owner's email or id: this struct is served publicly.
	AuthorName string `json:"author_name"`
}

type PublishedServiceSummary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Subtext     string `json:"subtext,omitempty"`
	Description string `json:"description,omitempty"`
	// AuthorName and PublishedAt are the card's byline, the same values the
	// publication itself carries: the owner's current name and the latest
	// publish, not the first publish the index is ordered by.
	AuthorName  string    `json:"author_name"`
	PublishedAt time.Time `json:"published_at"`
}

type PublishedIndexKey struct {
	FirstPublishedAt time.Time
	Slug             string
}

type PublishedIndexPage struct {
	Items []PublishedServiceSummary
	Next  *PublishedIndexKey
}
