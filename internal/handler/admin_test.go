package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

func TestCreateUserProvisionsALoggableAccount(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users",
		`{"email":"new@example.com","name":"New Person","password":"their-password"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	var created account.User
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" {
		t.Error("created user has no ID")
	}
	if created.Email != "new@example.com" || created.Name != "New Person" {
		t.Errorf("created user = %+v", created)
	}
	if created.IsAdmin {
		t.Error("accounts must not be admin unless explicitly requested")
	}

	// The provisioned account must actually be able to log in.
	login := postJSON(t, handler.Login(store, timeHour, testHasher), "/api/auth/login",
		`{"email":"new@example.com","password":"their-password"}`)
	if login.Code != http.StatusOK {
		t.Errorf("provisioned account could not log in: status %d, body %s",
			login.Code, login.Body.String())
	}
}

func TestProvisionedEmailIsCaseInsensitiveAtLogin(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users",
		`{"email":"  Mixed.Case@Example.com ","password":"their-password"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	login := postJSON(t, handler.Login(store, timeHour, testHasher), "/api/auth/login",
		`{"email":"MIXED.CASE@example.com","password":"their-password"}`)
	if login.Code != http.StatusOK {
		t.Errorf("mixed-case login failed: status %d, body %s", login.Code, login.Body.String())
	}
}

func TestCreateUserStoresOnlyAHash(t *testing.T) {
	store := newFakeAuthStore(t)
	postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users",
		`{"email":"new@example.com","password":"their-password"}`)

	rec := store.users["new@example.com"]
	if rec.hash == "their-password" {
		t.Fatal("password stored in plaintext")
	}
	if !auth.VerifyPassword(rec.hash, "their-password") {
		t.Error("stored hash does not verify the password")
	}
}

func TestCreateUserCanGrantAdmin(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users",
		`{"email":"admin2@example.com","password":"their-password","is_admin":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !store.users["admin2@example.com"].user.IsAdmin {
		t.Error("is_admin was not honored")
	}
}

func TestCreateUserValidatesInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing email", `{"password":"pw"}`, http.StatusBadRequest},
		{"missing password", `{"email":"a@example.com"}`, http.StatusBadRequest},
		{"empty password", `{"email":"a@example.com","password":""}`, http.StatusBadRequest},
		{"malformed json", `{"email":`, http.StatusBadRequest},
		{"duplicate email", `{"email":"user@example.com","password":"their-password"}`, http.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeAuthStore(t)
			before := len(store.created)
			rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users", tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body.String())
			}
			if len(store.created) != before {
				t.Error("an account was provisioned despite invalid input")
			}
		})
	}
}

func TestCreateUserRejectsWeakPasswords(t *testing.T) {
	long := strings.Repeat("a", 73)
	tests := []struct {
		name    string
		pw      string
		rule    string
		mention string
	}{
		{"eight characters", "spa383!!", fault.RuleMinLength, ""},
		{"73 bytes", long, fault.RuleMaxLength, "72"},
		{"common", "password123456", fault.RuleCommon, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeAuthStore(t)
			body, err := json.Marshal(map[string]string{
				"email":    "new@example.com",
				"password": tt.pw,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users", string(body))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rec.Code, rec.Body.String())
			}
			if len(store.created) != 0 {
				t.Fatal("an account was provisioned despite a weak password")
			}
			if strings.Contains(rec.Body.String(), tt.pw) {
				t.Fatalf("response contains the password: %s", rec.Body.String())
			}

			var got struct {
				Code   string `json:"code"`
				Detail struct {
					Faults []struct {
						Field   string `json:"field"`
						Rule    string `json:"rule"`
						Message string `json:"message"`
					} `json:"faults"`
				} `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v; body %s", err, rec.Body.String())
			}
			if got.Code != handler.ValidationErrorCode {
				t.Errorf("code = %q, want %q", got.Code, handler.ValidationErrorCode)
			}
			var matched bool
			for _, f := range got.Detail.Faults {
				if f.Field != "password" {
					t.Errorf("fault field = %q, want password", f.Field)
					continue
				}
				if f.Rule == tt.rule {
					matched = true
					if tt.mention != "" && !strings.Contains(f.Message, tt.mention) {
						t.Errorf("message %q does not mention %s", f.Message, tt.mention)
					}
				}
			}
			if !matched {
				t.Errorf("faults = %+v, want a password fault with rule %s", got.Detail.Faults, tt.rule)
			}
		})
	}
}
