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

// GeoJSON and compiled graphs shrink roughly tenfold, and a publication or
// prerendered isochrone runs to hundreds of kilobytes, so this is most of what
// a reader downloads.
//
// The ETag is left as the handler set it rather than suffixed per encoding.
// Vary keeps a cache from serving one encoding for the other, and a single tag
// means a revalidation matches whichever encoding the client holds.
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
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(header string) bool {
	for coding := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(coding, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		q, found := strings.CutPrefix(strings.TrimSpace(params), "q=")
		if !found {
			return true
		}
		if v, err := strconv.ParseFloat(q, 64); err == nil && v > 0 {
			return true
		}
	}
	return false
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
	status  int
	buf     []byte
	decided bool
	zw      *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(status int) {
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
		g.zw = gzipWriters.Get().(*gzip.Writer)
		g.zw.Reset(g.ResponseWriter)
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
