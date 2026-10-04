package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getWith(t *testing.T, h http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	return out
}

func TestJSONIsGzippedForClientsThatAcceptIt(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	const path = "/api/scenarios/ca-hsr/routes"

	plain := getWith(t, h, path, nil)
	if plain.Code != http.StatusOK || plain.Body.Len() < 4096 {
		t.Fatalf("precondition: status %d, %d bytes; want a large 200", plain.Code, plain.Body.Len())
	}
	if ce := plain.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("no Accept-Encoding, but Content-Encoding = %q", ce)
	}

	zipped := getWith(t, h, path, map[string]string{"Accept-Encoding": "br, gzip, deflate"})
	if zipped.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", zipped.Code)
	}
	if ce := zipped.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if cl := zipped.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q survived compression", cl)
	}
	if !strings.Contains(strings.Join(zipped.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", zipped.Header().Values("Vary"))
	}
	if got := gunzip(t, zipped.Body.Bytes()); !bytes.Equal(got, plain.Body.Bytes()) {
		t.Fatalf("decompressed body differs from the plain one")
	}
	// Route geometry is mostly full-precision coordinates, which gzip takes
	// down about 3.7x at any level; a floor of 3 catches compression that
	// quietly stopped happening without pinning the exact ratio.
	if zipped.Body.Len()*3 > plain.Body.Len() {
		t.Errorf("gzip body %d bytes against %d plain: under 3x smaller", zipped.Body.Len(), plain.Body.Len())
	}
	// A strong tag names exact bytes, so the gzipped body needs its own
	// (RFC 9110 §8.8.3).
	plainTag := plain.Header().Get("ETag")
	zippedTag := zipped.Header().Get("ETag")
	if want := strings.TrimSuffix(plainTag, `"`) + `-gzip"`; zippedTag != want {
		t.Fatalf("gzipped ETag = %q, want %q", zippedTag, want)
	}

	// Each copy revalidates with its own tag, and the 304 names the copy the
	// client holds, so a cache can freshen it.
	for _, tc := range []struct {
		name, accept, tag string
	}{
		{"gzipped copy", "gzip", zippedTag},
		{"plain copy", "", plainTag},
		{"plain copy, client now accepts gzip", "gzip", plainTag},
	} {
		rec := getWith(t, h, path, map[string]string{"Accept-Encoding": tc.accept, "If-None-Match": tc.tag})
		if rec.Code != http.StatusNotModified {
			t.Errorf("%s: status = %d, want 304", tc.name, rec.Code)
			continue
		}
		if got := rec.Header().Get("ETag"); got != tc.tag {
			t.Errorf("%s: 304 ETag = %q, want %q", tc.name, got, tc.tag)
		}
	}

	// Without gzip on offer, the gzipped copy's tag names nothing served.
	if rec := getWith(t, h, path, map[string]string{"If-None-Match": zippedTag}); rec.Code != http.StatusOK {
		t.Errorf("gzip tag from a client that no longer accepts gzip: status = %d, want 200", rec.Code)
	}
}

func TestCuratedTagsDifferByPath(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	a := getWith(t, h, "/api/scenarios", nil).Header().Get("ETag")
	b := getWith(t, h, "/api/scenarios/ca-hsr", nil).Header().Get("ETag")
	if a == b {
		t.Fatalf("two curated URLs share the tag %q", a)
	}
	// So a tag held for one URL cannot turn another's 404 into a 304.
	if rec := getWith(t, h, "/api/scenarios/no-such-scenario", map[string]string{"If-None-Match": a}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown slug with another URL's tag: status = %d, want 404", rec.Code)
	}
}

func TestGzipIsSkippedWhereItCannotHelp(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	accept := map[string]string{"Accept-Encoding": "gzip"}

	// Under the 1 KB floor, the gzip header and dictionary cost more than
	// they save.
	small := getWith(t, h, "/api/scenarios/no-such-scenario", accept)
	if small.Code != http.StatusNotFound || small.Header().Get("Content-Encoding") != "" {
		t.Errorf("small 404: status %d, Content-Encoding %q; want it sent plain", small.Code, small.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(small.Body.String(), "not found") {
		t.Errorf("small body lost: %q", small.Body.String())
	}

	etag := getWith(t, h, "/api/scenarios/ca-hsr/routes", nil).Header().Get("ETag")
	notModified := getWith(t, h, "/api/scenarios/ca-hsr/routes",
		map[string]string{"Accept-Encoding": "gzip", "If-None-Match": etag})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 ||
		notModified.Header().Get("Content-Encoding") != "" {
		t.Errorf("304: status %d, %d bytes, Content-Encoding %q; want an empty, unencoded 304",
			notModified.Code, notModified.Body.Len(), notModified.Header().Get("Content-Encoding"))
	}

	refused := getWith(t, h, "/api/scenarios/ca-hsr/routes", map[string]string{"Accept-Encoding": "gzip;q=0, identity"})
	if refused.Header().Get("Content-Encoding") != "" {
		t.Errorf("gzip;q=0 was compressed anyway")
	}
}

func TestGzipLeavesTheWorkerAPIAndNonJSONAlone(t *testing.T) {
	big := strings.Repeat("x", 8<<10)
	for _, tc := range []struct{ name, path, contentType string }{
		{"worker API", "/api/internal/isochrone-cache/lookup", "application/json"},
		{"not JSON", "/api/scenarios", "text/plain; charset=utf-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := compressJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, big)
			}))
			rec := getWith(t, h, tc.path, map[string]string{"Accept-Encoding": "gzip"})
			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none", ce)
			}
			if rec.Body.String() != big {
				t.Errorf("body altered: %d bytes", rec.Body.Len())
			}
		})
	}
}

// A body written in pieces straddling the floor is still sent whole: the
// bytes buffered while deciding are not lost when compression starts.
func TestGzipKeepsEveryByteOfAChunkedBody(t *testing.T) {
	chunks := []string{`{"a":"`, strings.Repeat("y", 1000), strings.Repeat("z", 3000), `"}`}
	h := compressJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		for _, c := range chunks {
			_, _ = io.WriteString(w, c)
		}
	}))
	rec := getWith(t, h, "/api/anything", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, Content-Encoding %q; want a gzipped 201", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if got := string(gunzip(t, rec.Body.Bytes())); got != strings.Join(chunks, "") {
		t.Errorf("decompressed %d bytes, want %d", len(got), len(strings.Join(chunks, "")))
	}
}
