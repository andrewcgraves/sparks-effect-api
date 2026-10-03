package account_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
)

func TestUserJSONIsTheMeAndLoginContract(t *testing.T) {
	u := account.User{
		ID:        "user-1",
		Email:     "ada@example.com",
		Name:      "Ada",
		IsAdmin:   true,
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	}

	got, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"id":"user-1","email":"ada@example.com","name":"Ada","is_admin":true,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:06Z"}`
	if string(got) != want {
		t.Errorf("User JSON:\n  got  %s\n  want %s", got, want)
	}

	var round account.User
	if err := json.Unmarshal(got, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round != u {
		t.Errorf("round-trip User = %+v, want %+v", round, u)
	}
}

func TestDisabledAtMarshalsAsRFC3339(t *testing.T) {
	disabled := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	u := account.User{
		ID:         "user-1",
		Email:      "ada@example.com",
		Name:       "Ada",
		IsAdmin:    false,
		CreatedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
		DisabledAt: &disabled,
	}

	got, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"id":"user-1","email":"ada@example.com","name":"Ada","is_admin":false,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:06Z","disabled_at":"2026-06-01T12:00:00Z"}`
	if string(got) != want {
		t.Errorf("User JSON:\n  got  %s\n  want %s", got, want)
	}

	var round account.User
	if err := json.Unmarshal(got, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.DisabledAt == nil || !round.DisabledAt.Equal(disabled) {
		t.Fatalf("DisabledAt = %v, want %s", round.DisabledAt, disabled.Format(time.RFC3339))
	}
	round.DisabledAt = nil
	u.DisabledAt = nil
	if round != u {
		t.Errorf("round-trip User = %+v, want %+v", round, u)
	}
}

func TestSessionIsNotAWireType(t *testing.T) {
	rt := reflect.TypeOf(account.Session{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		if tag := f.Tag.Get("json"); tag != "" {
			t.Errorf("Session.%s has json:%q; Session is the persisted token row, not an API body", f.Name, tag)
		}
	}
}
