package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Admin provisioning is the *only* way an account comes into existence — this
// is what stands in for a signup route in an invite-only system.
func TestCreateUserProvisionsALoggableAccount(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store), "/api/admin/users",
		`{"email":"new@example.com","name":"New Person","password":"their-password"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	var created transit.User
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
	login := postJSON(t, handler.Login(store, timeHour), "/api/auth/login",
		`{"email":"new@example.com","password":"their-password"}`)
	if login.Code != http.StatusOK {
		t.Errorf("provisioned account could not log in: status %d, body %s",
			login.Code, login.Body.String())
	}
}

// Email is the login key, so provisioning and login must agree on its
// normalized form — otherwise an account created with capitals is unreachable.
func TestProvisionedEmailIsCaseInsensitiveAtLogin(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store), "/api/admin/users",
		`{"email":"  Mixed.Case@Example.com ","password":"pw"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	login := postJSON(t, handler.Login(store, timeHour), "/api/auth/login",
		`{"email":"MIXED.CASE@example.com","password":"pw"}`)
	if login.Code != http.StatusOK {
		t.Errorf("mixed-case login failed: status %d, body %s", login.Code, login.Body.String())
	}
}

func TestCreateUserStoresOnlyAHash(t *testing.T) {
	store := newFakeAuthStore(t)
	postJSON(t, handler.CreateUser(store), "/api/admin/users",
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
	rec := postJSON(t, handler.CreateUser(store), "/api/admin/users",
		`{"email":"admin2@example.com","password":"pw","is_admin":true}`)
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
		{"duplicate email", `{"email":"user@example.com","password":"pw"}`, http.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeAuthStore(t)
			before := len(store.created)
			rec := postJSON(t, handler.CreateUser(store), "/api/admin/users", tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body.String())
			}
			if len(store.created) != before {
				t.Error("an account was provisioned despite invalid input")
			}
		})
	}
}
