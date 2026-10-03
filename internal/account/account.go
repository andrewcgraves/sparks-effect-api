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

type UserSummary struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	// No omitempty, unlike User: the admin list reports every account's
	// disabled state, so an enabled one carries an explicit null.
	DisabledAt     *time.Time `json:"disabled_at"`
	ServiceCount   int        `json:"service_count"`
	PublishedCount int        `json:"published_count"`
}

type UserPatch struct {
	IsAdmin  *bool `json:"is_admin"`
	Disabled *bool `json:"disabled"`
}

type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type TokenPurpose string

const (
	TokenPurposeInvite TokenPurpose = "invite"
	TokenPurposeReset  TokenPurpose = "reset"
)

type Token struct {
	TokenHash string
	UserID    string
	Purpose   TokenPurpose
	ExpiresAt time.Time
	UsedAt    *time.Time
}
