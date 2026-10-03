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

// CurrentHash is the hash the caller's current password was verified
// against. The change applies only while it is still the stored one, so two
// concurrent changes cannot both win, and only while KeepTokenHash is still a
// live session of the user's.
type PasswordChange struct {
	UserID        string
	CurrentHash   string
	NewHash       string
	KeepTokenHash string
}
