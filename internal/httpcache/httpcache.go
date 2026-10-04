package httpcache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	// A minute fresh, then ten more served past max-age while one request
	// revalidates: a republish reaches readers within a minute, and a burst of
	// readers after that costs one 304 rather than one full body each.
	Public = "public, max-age=60, stale-while-revalidate=600"
	// Anything computed from who is asking, and anything not deliberately
	// made public, is kept out of every cache — the browser's included.
	Private = "private, no-store"
)

// Every tag folds in the build, so a deploy that changes what a response
// looks like — a new field, a reworded error — invalidates every tag minted
// before it, even where the data the tag is derived from did not move.
type Tagger struct {
	build string
}

// An empty build — a local `go run`, a test — falls back to a value unique to
// this process. That costs every restart its cache, which is the right side to
// err on: a tag that outlived a change to the response would serve the old
// body as a 304 indefinitely.
func NewTagger(build string) Tagger {
	if build == "" {
		build = "process-" + rand.Text()
	}
	return Tagger{build: build}
}

// A strong validator. The parts are separated by a byte no part contains, so
// ("ab", "c") and ("a", "bc") tag differently.
func (t Tagger) ETag(parts ...string) string {
	h := sha256.New()
	h.Write([]byte(t.build))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

// Call only once the request is known to resolve to the representation etag
// names: a match answers 304 with the public headers, and the caller must
// write nothing further.
func NotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	if !matches(r.Header.Get("If-None-Match"), etag) {
		return false
	}
	MarkPublic(w, etag)
	w.WriteHeader(http.StatusNotModified)
	return true
}

func MarkPublic(w http.ResponseWriter, etag string) {
	w.Header().Set("Cache-Control", Public)
	w.Header().Set("ETag", etag)
}

// If-None-Match compares weakly (RFC 9110 §13.1.2), so a W/ a cache added in
// transit still matches. "*" is not honoured: PathTagged checks before its handler
// has decided whether the resource exists, and no browser sends it on a GET.
func matches(header, etag string) bool {
	for tag := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(tag), "W/") == etag {
			return true
		}
	}
	return false
}

// Every response starts out private. A public read opts in by overwriting the
// header, so a new route is uncacheable until someone decides otherwise.
func DefaultPrivate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", Private)
		next.ServeHTTP(w, r)
	})
}

// For a handler whose answer at each path is fixed for the process's lifetime:
// the curated scenario reads, which move only on a deploy. version names
// whatever besides the build shapes those answers. A match is answered before
// the handler runs, which is sound because the tag is the path's own: a client
// holds it just when that path answered 200 under this build, and under the
// same build it still does.
func PathTagged(tags Tagger, version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := tags.ETag(version, r.URL.Path)
		if NotModified(w, r, etag) {
			return
		}
		next.ServeHTTP(&publicOnOK{ResponseWriter: w, etag: etag}, r)
	})
}

// Marks a response public only once its status is known to be 200, so a 404
// for an unknown slug or a 500 is never stored by a shared cache.
type publicOnOK struct {
	http.ResponseWriter
	etag        string
	wroteHeader bool
}

func (p *publicOnOK) WriteHeader(status int) {
	if !p.wroteHeader {
		p.wroteHeader = true
		if status == http.StatusOK {
			MarkPublic(p.ResponseWriter, p.etag)
		}
	}
	p.ResponseWriter.WriteHeader(status)
}

func (p *publicOnOK) Write(b []byte) (int, error) {
	if !p.wroteHeader {
		p.WriteHeader(http.StatusOK)
	}
	return p.ResponseWriter.Write(b)
}

func (p *publicOnOK) Unwrap() http.ResponseWriter { return p.ResponseWriter }
