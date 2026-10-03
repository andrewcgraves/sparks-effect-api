package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

const testWebsiteURL = "https://site.example"

type tokenRecord struct {
	token account.Token
	used  bool
}

type fakeTokenStore struct {
	*fakeAuthStore
	tokens   map[string]*tokenRecord
	disabled map[string]bool
	redeemed []string
}

func newFakeTokenStore(t *testing.T) *fakeTokenStore {
	t.Helper()
	return &fakeTokenStore{
		fakeAuthStore: newFakeAuthStore(t),
		tokens:        map[string]*tokenRecord{},
		disabled:      map[string]bool{},
	}
}

func (f *fakeTokenStore) userByID(id string) (account.User, bool) {
	for _, rec := range f.users {
		if rec.user.ID == id {
			return rec.user, true
		}
	}
	return account.User{}, false
}

func (f *fakeTokenStore) GetUserByID(_ context.Context, id string) (account.User, bool, error) {
	u, ok := f.userByID(id)
	return u, ok, nil
}

func (f *fakeTokenStore) CreateInvite(ctx context.Context, u account.User, t account.Token) error {
	if err := f.CreateUser(ctx, u, ""); err != nil {
		return err
	}
	t.UserID = u.ID
	f.tokens[t.TokenHash] = &tokenRecord{token: t}
	return nil
}

func (f *fakeTokenStore) CreateResetToken(_ context.Context, t account.Token) error {
	for h, rec := range f.tokens {
		if rec.token.UserID == t.UserID && rec.token.Purpose == account.TokenPurposeReset && !rec.used {
			delete(f.tokens, h)
		}
	}
	f.tokens[t.TokenHash] = &tokenRecord{token: t}
	return nil
}

func (f *fakeTokenStore) live(hash string) (*tokenRecord, account.User, bool) {
	rec, ok := f.tokens[hash]
	if !ok || rec.used || !rec.token.ExpiresAt.After(time.Now()) || f.disabled[rec.token.UserID] {
		return nil, account.User{}, false
	}
	u, ok := f.userByID(rec.token.UserID)
	return rec, u, ok
}

func (f *fakeTokenStore) GetAccountToken(_ context.Context, hash string) (account.Token, account.User, bool, error) {
	rec, u, ok := f.live(hash)
	if !ok {
		return account.Token{}, account.User{}, false, nil
	}
	return rec.token, u, true, nil
}

func (f *fakeTokenStore) RedeemAccountToken(_ context.Context, hash, passwordHash string, s account.Session) (account.User, bool, error) {
	rec, u, ok := f.live(hash)
	if !ok {
		return account.User{}, false, nil
	}
	rec.used = true
	f.users[u.Email] = userRecord{user: u, hash: passwordHash}
	for h, sess := range f.sessions {
		if sess.UserID == u.ID {
			delete(f.sessions, h)
		}
	}
	s.UserID = u.ID
	f.sessions[s.TokenHash] = s
	f.redeemed = append(f.redeemed, hash)
	return u, true, nil
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != testWebsiteURL+"/set-password" {
		t.Fatalf("link %q does not point at the website's set-password page", link)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("link %q carries no token", link)
	}
	return tok
}

func getPath(t *testing.T, h http.Handler, pattern, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET "+pattern, h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rec
}

func postPath(t *testing.T, h http.Handler, pattern, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("POST "+pattern, h)
	return postJSON(t, mux, path, body)
}

const tokenPattern = "/api/auth/tokens/{token}"

func invite(t *testing.T, store *fakeTokenStore, body string) (account.User, string) {
	t.Helper()
	rec := postJSON(t, handler.CreateInvite(store, testWebsiteURL), "/api/admin/invites", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User account.User `json:"user"`
		URL  string       `json:"url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	return resp.User, tokenFromLink(t, resp.URL)
}

func TestInviteReturnsTheUserAndALinkThatGreetsThem(t *testing.T) {
	store := newFakeTokenStore(t)
	before := time.Now()
	user, tok := invite(t, store, `{"email":" New@Example.com ","name":"New Person","is_admin":true}`)

	if user.ID == "" || user.Email != "new@example.com" || user.Name != "New Person" || !user.IsAdmin {
		t.Errorf("invited user = %+v", user)
	}
	if store.users["new@example.com"].hash != "" {
		t.Error("an invited user must have no usable password")
	}
	if _, ok := store.tokens[tok]; ok {
		t.Fatal("the raw token was stored; only its hash may be")
	}

	rec := getPath(t, handler.AccountToken(store), tokenPattern, "/api/auth/tokens/"+tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Purpose   string    `json:"purpose"`
		Email     string    `json:"email"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Purpose != "invite" || got.Email != "new@example.com" {
		t.Errorf("GET token = %+v", got)
	}
	wantExpiry := before.Add(7 * 24 * time.Hour)
	if got.ExpiresAt.Before(wantExpiry) || got.ExpiresAt.After(wantExpiry.Add(time.Minute)) {
		t.Errorf("invite expires_at = %v, want about %v", got.ExpiresAt, wantExpiry)
	}
}

func TestInviteValidatesInput(t *testing.T) {
	for name, tt := range map[string]struct {
		body string
		want int
	}{
		"missing email":   {`{"name":"x"}`, http.StatusBadRequest},
		"malformed json":  {`{"email":`, http.StatusBadRequest},
		"duplicate email": {`{"email":"user@example.com"}`, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeTokenStore(t)
			rec := postJSON(t, handler.CreateInvite(store, testWebsiteURL), "/api/admin/invites", tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body.String())
			}
			if len(store.tokens) != 0 || len(store.created) != 0 {
				t.Error("an invite was created despite invalid input")
			}
		})
	}
}

func TestRedeemingATokenSetsThePasswordAndReturnsASession(t *testing.T) {
	store := newFakeTokenStore(t)
	_, tok := invite(t, store, `{"email":"new@example.com"}`)

	rec := postPath(t, handler.RedeemAccountToken(store, time.Hour, testHasher), tokenPattern,
		"/api/auth/tokens/"+tok, `{"password":"a-strong-new-password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST token: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token     string       `json:"token"`
		ExpiresAt time.Time    `json:"expires_at"`
		User      account.User `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.User.Email != "new@example.com" || resp.ExpiresAt.IsZero() {
		t.Errorf("response = %+v", resp)
	}
	if s, ok := store.sessions[auth.HashToken(resp.Token)]; !ok || s.UserID != resp.User.ID {
		t.Errorf("returned token is not a session for the user: %+v ok=%v", s, ok)
	}

	login := postJSON(t, handler.Login(store, time.Hour, testHasher), "/api/auth/login",
		`{"email":"new@example.com","password":"a-strong-new-password"}`)
	if login.Code != http.StatusOK {
		t.Errorf("login with the new password: status %d", login.Code)
	}

	again := postPath(t, handler.RedeemAccountToken(store, time.Hour, testHasher), tokenPattern,
		"/api/auth/tokens/"+tok, `{"password":"another-strong-password"}`)
	if again.Code != http.StatusNotFound {
		t.Errorf("second POST: status %d, want 404", again.Code)
	}
}

func TestRedeemingATokenEnforcesThePasswordPolicy(t *testing.T) {
	store := newFakeTokenStore(t)
	_, tok := invite(t, store, `{"email":"new@example.com"}`)

	rec := postPath(t, handler.RedeemAccountToken(store, time.Hour, testHasher), tokenPattern,
		"/api/auth/tokens/"+tok, `{"password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weak password: status %d, want 422; body %s", rec.Code, rec.Body.String())
	}
	if len(store.redeemed) != 0 {
		t.Fatal("a weak password consumed the token")
	}
}

func TestUnknownExpiredAndDisabledTokensAllAnswer404(t *testing.T) {
	store := newFakeTokenStore(t)
	expired, expiredHash, _ := auth.NewToken()
	store.tokens[expiredHash] = &tokenRecord{token: account.Token{
		TokenHash: expiredHash, UserID: "user-1", Purpose: account.TokenPurposeReset,
		ExpiresAt: time.Now().Add(-time.Minute),
	}}
	disabled, disabledHash, _ := auth.NewToken()
	store.tokens[disabledHash] = &tokenRecord{token: account.Token{
		TokenHash: disabledHash, UserID: "user-2", Purpose: account.TokenPurposeReset,
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	store.disabled["user-2"] = true

	var bodies []string
	for name, tok := range map[string]string{"unknown": "not-a-token", "expired": expired, "disabled": disabled} {
		get := getPath(t, handler.AccountToken(store), tokenPattern, "/api/auth/tokens/"+tok)
		post := postPath(t, handler.RedeemAccountToken(store, time.Hour, testHasher), tokenPattern,
			"/api/auth/tokens/"+tok, `{"password":"a-strong-new-password"}`)
		if get.Code != http.StatusNotFound || post.Code != http.StatusNotFound {
			t.Errorf("%s: GET %d, POST %d, want 404 both", name, get.Code, post.Code)
		}
		bodies = append(bodies, get.Body.String(), post.Body.String())
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("404 bodies differ: %q vs %q", b, bodies[0])
		}
	}
}

func TestResetLinkInvalidatesTheEarlierOne(t *testing.T) {
	store := newFakeTokenStore(t)
	h := handler.CreateResetLink(store, testWebsiteURL)
	const pattern = "/api/admin/users/{id}/reset-link"

	issue := func() string {
		t.Helper()
		rec := postPath(t, h, pattern, "/api/admin/users/user-1/reset-link", "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("reset-link: status %d, body %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return tokenFromLink(t, resp.URL)
	}
	first, second := issue(), issue()

	if rec := getPath(t, handler.AccountToken(store), tokenPattern, "/api/auth/tokens/"+first); rec.Code != http.StatusNotFound {
		t.Errorf("earlier reset link: status %d, want 404", rec.Code)
	}
	rec := getPath(t, handler.AccountToken(store), tokenPattern, "/api/auth/tokens/"+second)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"purpose":"reset"`) {
		t.Errorf("newer reset link: status %d body %s", rec.Code, rec.Body.String())
	}
	tok := store.tokens[auth.HashToken(second)].token
	if until := time.Until(tok.ExpiresAt); until < 59*time.Minute || until > time.Hour {
		t.Errorf("reset link expires in %v, want an hour", until)
	}

	if rec := postPath(t, h, pattern, "/api/admin/users/nobody/reset-link", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", rec.Code)
	}
}
