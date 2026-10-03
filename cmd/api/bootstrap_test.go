package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	internlog "github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	if os.Getenv("SPA383_RUN_API") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// *postgres.Repo must keep satisfying the bootstrap seam.
var _ bootstrapUserStore = (*postgres.Repo)(nil)

type fakeBootstrapStore struct {
	exists  bool
	gets    int
	created []account.User
	hashes  []string
}

func (f *fakeBootstrapStore) GetUserByEmail(context.Context, string) (account.User, bool, error) {
	f.gets++
	if !f.exists {
		return account.User{}, false, nil
	}
	return account.User{Email: "admin@example.com", IsAdmin: true}, true, nil
}

func (f *fakeBootstrapStore) CreateUser(_ context.Context, u account.User, hash string) error {
	f.created = append(f.created, u)
	f.hashes = append(f.hashes, hash)
	return nil
}

func TestBootstrapDisabledSkipsValidation(t *testing.T) {
	store := &fakeBootstrapStore{}
	cases := []config.Config{
		{BootstrapAdminEmail: "admin@example.com"},
		{BootstrapAdminPassword: "shortpw"},
		{BootstrapAdminEmail: "   ", BootstrapAdminPassword: "password123456"},
	}
	for _, cfg := range cases {
		store.gets = 0
		store.created = nil
		if err := bootstrapAdmin(context.Background(), cfg, store, internlog.Discard()); err != nil {
			t.Fatalf("disabled bootstrap (%+v) returned %v", cfg, err)
		}
		if store.gets != 0 || len(store.created) != 0 {
			t.Fatalf("disabled bootstrap touched the store: gets=%d created=%d", store.gets, len(store.created))
		}
	}
}

func TestBootstrapRejectsWeakPasswordWithoutCreating(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		want string
	}{
		{"too short", "shortpw", "password must be at least 12 characters"},
		{"common", "password123456", "password is too common"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeBootstrapStore{}
			var logs bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&logs, nil))
			err := bootstrapAdmin(context.Background(), config.Config{
				BootstrapAdminEmail:    "admin@example.com",
				BootstrapAdminPassword: tt.pw,
			}, store, lg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain the failure", err.Error())
			}
			if strings.Contains(err.Error(), tt.pw) || strings.Contains(logs.String(), tt.pw) {
				t.Errorf("error or log contains the password: err=%q logs=%s", err.Error(), logs.String())
			}
			if len(store.created) != 0 {
				t.Errorf("CreateUser was called %d times", len(store.created))
			}
		})
	}
}

func TestBootstrapExistingUserIsLeftUnchanged(t *testing.T) {
	store := &fakeBootstrapStore{exists: true}
	var logs bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logs, nil))
	const pw = "shortpw"
	err := bootstrapAdmin(context.Background(), config.Config{
		BootstrapAdminEmail:    "admin@example.com",
		BootstrapAdminPassword: pw,
	}, store, lg)
	if err != nil {
		t.Fatalf("existing user: %v", err)
	}
	if len(store.created) != 0 {
		t.Fatal("CreateUser was called for an account that already exists")
	}
	if !strings.Contains(logs.String(), "leaving it unchanged") {
		t.Errorf("log = %s, want the unchanged notice", logs.String())
	}
	if strings.Contains(logs.String(), pw) {
		t.Errorf("log contains the password: %s", logs.String())
	}
}

func TestBootstrapStrongPasswordCreatesAdmin(t *testing.T) {
	store := &fakeBootstrapStore{}
	const pw = "their-password"
	err := bootstrapAdmin(context.Background(), config.Config{
		BootstrapAdminEmail:    "  Admin@Example.com ",
		BootstrapAdminPassword: pw,
		PasswordHashCost:       bcrypt.MinCost,
	}, store, internlog.Discard())
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d users, want 1", len(store.created))
	}
	got := store.created[0]
	if got.Email != "admin@example.com" || got.Name != "Bootstrap Admin" || !got.IsAdmin {
		t.Errorf("created = %+v", got)
	}
	if !auth.VerifyPassword(store.hashes[0], pw) {
		t.Error("stored hash does not verify the password")
	}
}

func TestBootstrapWeakPasswordProcessExits(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("set TEST_DATABASE_URL to run the bootstrap process test")
	}
	dbURL := testdb.Empty(t)
	email := fmt.Sprintf("spa383-boot-%d@example.com", time.Now().UnixNano())
	const weak = "shortpw"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"SPA383_RUN_API=1",
		"DATABASE_URL="+dbURL,
		"BOOTSTRAP_ADMIN_EMAIL="+email,
		"BOOTSTRAP_ADMIN_PASSWORD="+weak,
		"LOG_LEVEL=info",
		"VERBOSE=",
	)
	var output lockedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err = cmd.Run()
	logs := output.String()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("api process timed out; a weak bootstrap password must exit before listen\n%s", logs)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("process error = %v, want non-zero exit\n%s", err, logs)
	}
	if strings.Contains(logs, weak) {
		t.Fatalf("logs contain the bootstrap password:\n%s", logs)
	}
	if !strings.Contains(logs, "failed to provision bootstrap admin") ||
		!strings.Contains(logs, "password must be at least 12 characters") {
		t.Fatalf("logs missing the failure reason:\n%s", logs)
	}
	if strings.Contains(logs, "listening") || strings.Contains(logs, "failed to load transit data") {
		t.Fatalf("logs show the process reached listen or died before bootstrap:\n%s", logs)
	}

	repo, connErr := postgres.Connect(context.Background(), dbURL, 1)
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}
	t.Cleanup(repo.Close)
	_, exists, lookupErr := repo.GetUserByEmail(context.Background(), email)
	if lookupErr != nil {
		t.Fatalf("GetUserByEmail: %v", lookupErr)
	}
	if exists {
		t.Fatal("weak bootstrap password created a user")
	}
}

// lockedBuffer collects stdout and stderr without a race under -race.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
