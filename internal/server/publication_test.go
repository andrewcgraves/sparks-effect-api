package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestGetServicePublicationIsNotRegistered(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/services/some-slug/publication", userToken)
	// PUT and DELETE are registered on this path, so the mux answers every
	// other method with 405. That is the unimplemented GET: no handler runs,
	// and the body is not a publication.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 from the mux; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q, want the mux 405 rather than a handler body", rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Body.String(), "compile_job_id") || strings.Contains(rec.Body.String(), "user_service_id") {
		t.Fatalf("GET publication returned a publication body: %s", rec.Body.String())
	}
}
