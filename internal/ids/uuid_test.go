package ids_test

import (
	"regexp"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for range 100 {
		id, err := ids.NewUUID()
		if err != nil {
			t.Fatalf("NewUUID: %v", err)
		}
		if !uuidV4.MatchString(id) {
			t.Fatalf("NewUUID returned %q, not a canonical v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("NewUUID repeated %q", id)
		}
		seen[id] = true
	}
}
