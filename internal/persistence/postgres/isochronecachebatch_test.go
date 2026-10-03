package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

func fixturePut(t *testing.T) []handler.CachedIsochrone {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "handler", "testdata", "cache-put-sj-240-bike.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var body struct {
		Entries []handler.CachedIsochrone `json:"entries"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for i := range body.Entries {
		body.Entries[i].Key.CompileJobID = routingCompileJobID
	}
	return body.Entries
}

// SPA-332: the largest real chain's put lands in full and reads back.
func TestIsochroneCachePut_realChainFixture(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	entries := fixturePut(t)

	if err := repo.PutIsochroneCache(ctx, entries); err != nil {
		t.Fatalf("put: %v", err)
	}

	keys := make([]handler.IsochroneKey, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	found, err := repo.GetIsochroneCache(ctx, keys)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(found) != len(entries) {
		t.Errorf("read back %d rows, want %d", len(found), len(entries))
	}
}

// SPA-332: one rejected entry loses the whole put, which is kept on purpose.
func TestIsochroneCachePut_oneBadEntryLosesTheBatch(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)
	entries := fixturePut(t)
	entries[len(entries)-1].Key.DepartsOn = "not-a-date"

	if err := repo.PutIsochroneCache(ctx, entries); err == nil {
		t.Fatal("put with a malformed entry succeeded, want an error")
	}

	found, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{entries[0].Key})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(found) != 0 {
		t.Error("a valid entry from the rejected put was written")
	}
}
