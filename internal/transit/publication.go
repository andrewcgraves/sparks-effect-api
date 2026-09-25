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
}

type PublishedServiceSummary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Subtext     string `json:"subtext,omitempty"`
	Description string `json:"description,omitempty"`
}
