package testdb

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewNameIsUniqueWithinAProcessAndKeepsTheUnixPrefix(t *testing.T) {
	a := newName()
	b := newName()
	if a == b {
		t.Fatalf("newName collided: %s", a)
	}
	pid := strconv.Itoa(os.Getpid())
	if !strings.Contains(a, "_"+pid+"_") {
		t.Fatalf("newName %q does not include pid %s", a, pid)
	}
	got := createdAt(a)
	now := time.Now().Unix()
	if got.Unix() != now && got.Unix() != now-1 {
		t.Fatalf("createdAt(%q) = %v, want the current unix second", a, got)
	}
}
