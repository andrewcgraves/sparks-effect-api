package account

import "time"

type User struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// DisabledAt drops User's omitempty: the admin list reports every account's
// disabled state, so an enabled one carries an explicit null.
type UserSummary struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	IsAdmin        bool       `json:"is_admin"`
	CreatedAt      time.Time  `json:"created_at"`
	DisabledAt     *time.Time `json:"disabled_at"`
	ServiceCount   int        `json:"service_count"`
	PublishedCount int        `json:"published_count"`
}

type UserPatch struct {
	IsAdmin  *bool
	Disabled *bool
}

type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}
