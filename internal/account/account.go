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

type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}
