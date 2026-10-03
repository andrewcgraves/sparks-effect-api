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
}

// Expiry is lazy: nothing rewrites a pending row when its deadline passes, so
// every reader asks this rather than trusting Status.
func (h ServiceHandover) EffectiveStatus(now time.Time) string {
	if h.Status == HandoverPending && !now.Before(h.ExpiresAt) {
		return HandoverExpired
	}
	return h.Status
}
