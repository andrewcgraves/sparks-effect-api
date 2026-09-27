package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetServicePublicationTakesNoIdentity(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	// stubAuthDeps publishes nothing, so reaching the handler is a 404 with its
	// JSON body — not the gate's 401, and not the mux's 405 of an unregistered
	// method. A token the server does not recognise is ignored rather than
	// refused: no auth middleware of any kind sits on this route.
	for _, token := range []string{"", userToken, adminToken, "not-a-session"} {
		rec := request(t, h, http.MethodGet, "/api/services/some-slug/publication", token)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("token %q: status = %d, want 404 from the handler; body %s", token, rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("token %q: body is not the handler's JSON: %v; %s", token, err, rec.Body.String())
		}
		if body["error"] != "service not found" {
			t.Fatalf("token %q: error = %q, want the unknown-service answer", token, body["error"])
		}
	}
}
