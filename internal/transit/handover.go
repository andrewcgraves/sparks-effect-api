package transit

import "time"

const (
	HandoverPending   = "pending"
	HandoverAccepted  = "accepted"
	HandoverDeclined  = "declined"
	HandoverCancelled = "cancelled"
	HandoverExpired   = "expired"
)

type ServiceHandover struct {
	ID            string
	UserServiceID string
	ServiceSlug   string
	ServiceName   string
	FromUserID    string
	FromName      string
	ToUserID      string
	ToName        string
	Status        string
	CreatedAt     time.Time
	DecidedAt     *time.Time
	ExpiresAt     time.Time
	// The admin who moved the service without an offer (SPA-390); nil for a
	// handover the recipient accepted.
	ForcedBy *string
}
