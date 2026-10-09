package server

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Below this the gzip header and an empty dictionary cost about what they
// save, and an error body or a job id is not worth the CPU.
const gzipMinBytes = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// A publication or prerendered isochrone runs to hundreds of kilobytes, most
// of it full-precision coordinates that gzip takes down about 3.7x. The rest of
// the JSON, which repeats more, shrinks 5–8x.
//
// A strong ETag names exact bytes (RFC 9110 §8.8.3), so a gzipped body gets
// the handler's tag with gzipTagSuffix inside the quotes. The suffix is taken
// off an incoming If-None-Match before the handler compares it, and put back
// on the 304, so the handlers mint and compare one tag per representation and
// never learn about encodings.
func compressJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The worker API is its own contract with one client (SPA-332), and
		// compressing it is a protocol change for both sides, not a tweak here.
		if strings.HasPrefix(r.URL.Path, "/api/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w, revalidatingGzip: stripGzipTags(r)}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

const gzipTagSuffix = "-gzip"

func stripGzipTags(r *http.Request) bool {
	inm := r.Header.Get("If-None-Match")
	if !strings.Contains(inm, gzipTagSuffix+`"`) {
		return false
	}
	r.Header.Set("If-None-Match", strings.ReplaceAll(inm, gzipTagSuffix+`"`, `"`))
	return true
}

// A weak tag already promises only equivalent content, not equal bytes, so
// it is left alone.
func gzipTag(etag string) string {
	if etag == "" || strings.HasPrefix(etag, "W/") {
		return etag
	}
	return strings.TrimSuffix(etag, `"`) + gzipTagSuffix + `"`
}

func acceptsGzip(header string) bool {
	// An explicit gzip entry decides; the wildcard speaks only for codings
	// the client did not name (RFC 9110 §12.5.3).
	wildcard := false
	for coding := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(coding, ";")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "gzip":
			return qualityAboveZero(params)
		case "*":
			wildcard = qualityAboveZero(params)
		}
	}
	return wildcard
}

func qualityAboveZero(params string) bool {
	params = strings.TrimSpace(params)
	if len(params) < 2 || !strings.EqualFold(params[:2], "q=") {
		return true
	}
	v, err := strconv.ParseFloat(params[2:], 64)
	return err == nil && v > 0
}

func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// Holds the status and the first gzipMinBytes of body back until it knows
// whether the response is worth compressing, then commits to one or the
// other for the rest of it.
type gzipResponseWriter struct {
	http.ResponseWriter
	revalidatingGzip bool
	status           int
	buf              []byte
	decided          bool
	zw               *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	// An informational status precedes the real one rather than being it.
	if status < http.StatusOK {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	if g.status == 0 {
		g.status = status
	}
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.decided {
		if g.zw != nil {
			return g.zw.Write(b)
		}
		return g.ResponseWriter.Write(b)
	}
	g.buf = append(g.buf, b...)
	if len(g.buf) < gzipMinBytes {
		return len(b), nil
	}
	if err := g.commit(); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (g *gzipResponseWriter) commit() error {
	g.decided = true
	h := g.Header()
	if len(g.buf) >= gzipMinBytes && isJSON(h.Get("Content-Type")) && h.Get("Content-Encoding") == "" {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Set("ETag", gzipTag(h.Get("ETag")))
		g.zw = gzipWriters.Get().(*gzip.Writer)
		g.zw.Reset(g.ResponseWriter)
	} else if g.status == http.StatusNotModified && g.revalidatingGzip {
		// The client matched on its gzipped copy's tag; the 304 must name
		// that copy for a cache to freshen it.
		h.Set("ETag", gzipTag(h.Get("ETag")))
	}
	g.ResponseWriter.WriteHeader(g.status)
	buf := g.buf
	g.buf = nil
	if len(buf) == 0 {
		return nil
	}
	var err error
	if g.zw != nil {
		_, err = g.zw.Write(buf)
	} else {
		_, err = g.ResponseWriter.Write(buf)
	}
	return err
}

func (g *gzipResponseWriter) finish() {
	// A handler that wrote nothing at all is left to net/http's implicit 200.
	if g.status == 0 {
		return
	}
	if !g.decided {
		_ = g.commit()
	}
	if g.zw != nil {
		_ = g.zw.Close()
		g.zw.Reset(io.Discard)
		gzipWriters.Put(g.zw)
		g.zw = nil
	}
}
