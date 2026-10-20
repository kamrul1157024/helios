package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// See docs/specs/65-fast-files.md, Phase 0 and 1.2.
//
// The files routes get two things no other route needs: a Server-Timing header,
// so the budget table can be measured from the desktop devtools with no new
// code, and gzip, because source code shrinks to a quarter of its size and these
// are the routes that carry it over tunnels. Both live in one writer so that a
// handler only ever sees one wrapper, and jsonResponse can find it.

// gzipMinBytes is the body size below which compression is not worth the
// header. A folder listing of three entries gains nothing from it.
const gzipMinBytes = 1024

var gzipWriters = sync.Pool{
	New: func() any {
		// BestSpeed compresses at over 100 MB/s on one core. On localhost the
		// cost is small; over a tunnel the bytes saved are the whole point.
		zw, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		if err != nil {
			panic(fmt.Sprintf("gzip level: %v", err))
		}
		return zw
	},
}

type timingKey struct{}

// serverTiming collects the named parts of one request. Safe for one handler
// goroutine plus the writer, which is all a request has.
type serverTiming struct {
	mu    sync.Mutex
	start time.Time
	parts []timingPart
}

type timingPart struct {
	name string
	dur  time.Duration
}

// timingFrom returns the request's timing, or nil when the route is not a
// files route. Every method tolerates a nil receiver, so handlers shared with
// other routes need no guard.
func timingFrom(ctx context.Context) *serverTiming {
	t, _ := ctx.Value(timingKey{}).(*serverTiming)
	return t
}

// add records one part. Parts with the same name accumulate, so a handler that
// stats twice reports the sum rather than two entries.
func (t *serverTiming) add(name string, d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.parts {
		if t.parts[i].name == name {
			t.parts[i].dur += d
			return
		}
	}
	t.parts = append(t.parts, timingPart{name: name, dur: d})
}

// since records the time from start to now under name. Written for defer:
// `defer timing.since("stat", time.Now())`.
func (t *serverTiming) since(name string, start time.Time) {
	t.add(name, time.Since(start))
}

func (t *serverTiming) header() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "total;dur=%.2f", ms(time.Since(t.start)))
	for _, p := range t.parts {
		fmt.Fprintf(&b, ", %s;dur=%.2f", p.name, ms(p.dur))
	}
	return b.String()
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// fastFiles wraps a files route with Server-Timing and gzip.
func fastFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timing := &serverTiming{start: time.Now()}
		fw := &fastWriter{
			ResponseWriter: w,
			timing:         timing,
			acceptsGzip:    acceptsGzip(r),
		}
		defer fw.finish()
		next.ServeHTTP(fw, r.WithContext(context.WithValue(r.Context(), timingKey{}, timing)))
	})
}

func fastFilesFunc(next http.HandlerFunc) http.Handler { return fastFiles(next) }

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		// "gzip;q=0" is a refusal, not an offer.
		if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// fastWriter holds the response back until it knows whether to compress it.
//
// The decision needs the body size, and a handler does not announce one, so
// the first gzipMinBytes are buffered. A body that never grows past that goes
// out plain when the handler returns; one that does is compressed from the
// start. Content-Length, when a handler sets it, decides at once.
type fastWriter struct {
	http.ResponseWriter
	timing      *serverTiming
	acceptsGzip bool

	status      int
	wroteHeader bool // the handler called WriteHeader (or Write)
	committed   bool // headers have gone to the client
	buf         bytes.Buffer
	zw          *gzip.Writer
}

func (fw *fastWriter) WriteHeader(code int) {
	if fw.wroteHeader {
		return
	}
	fw.wroteHeader, fw.status = true, code
	if !bodyAllowed(code) {
		fw.commit(false)
		return
	}
	if n, err := strconv.Atoi(fw.Header().Get("Content-Length")); err == nil {
		fw.commit(n > gzipMinBytes && fw.compressible())
	}
}

func (fw *fastWriter) Write(p []byte) (int, error) {
	if !fw.wroteHeader {
		fw.WriteHeader(http.StatusOK)
	}
	if fw.committed {
		if fw.zw != nil {
			return fw.zw.Write(p)
		}
		return fw.ResponseWriter.Write(p)
	}
	fw.buf.Write(p)
	if fw.buf.Len() > gzipMinBytes {
		fw.commit(fw.compressible())
		if err := fw.drain(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush sends what is buffered. A route that flushes is streaming, and a
// stream is never held back for a size decision.
func (fw *fastWriter) Flush() {
	if !fw.committed {
		if !fw.wroteHeader {
			fw.WriteHeader(http.StatusOK)
		}
		fw.commit(false)
		if fw.drain() != nil {
			return
		}
	}
	if fw.zw != nil {
		_ = fw.zw.Flush()
	}
	if f, ok := fw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection underneath.
func (fw *fastWriter) Unwrap() http.ResponseWriter { return fw.ResponseWriter }

func (fw *fastWriter) compressible() bool {
	if !fw.acceptsGzip || fw.Header().Get("Content-Encoding") != "" {
		return false
	}
	ctype := fw.Header().Get("Content-Type")
	if ctype == "" {
		ctype = http.DetectContentType(fw.buf.Bytes())
	}
	return compressibleType(ctype)
}

// compressibleType admits text and JSON, and SVG as the one image that is
// text. Never an event stream: SSE is a stream of small writes a client must
// see as they happen.
func compressibleType(ctype string) bool {
	media, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		return false
	}
	switch {
	case media == "text/event-stream":
		return false
	case strings.HasPrefix(media, "text/"):
		return true
	case media == "image/svg+xml":
		return true
	case media == "application/json", strings.HasSuffix(media, "+json"),
		media == "application/javascript", media == "application/xml",
		strings.HasSuffix(media, "+xml"):
		return true
	}
	return false
}

// commit writes the headers, choosing the encoding.
func (fw *fastWriter) commit(compress bool) {
	if fw.committed {
		return
	}
	fw.committed = true
	h := fw.Header()
	h.Set("Server-Timing", fw.timing.header())
	if compress {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Del("Content-Length")
		zw := gzipWriters.Get().(*gzip.Writer)
		zw.Reset(fw.ResponseWriter)
		fw.zw = zw
	}
	fw.ResponseWriter.WriteHeader(fw.status)
}

func (fw *fastWriter) drain() error {
	if fw.buf.Len() == 0 {
		return nil
	}
	var err error
	if fw.zw != nil {
		_, err = fw.zw.Write(fw.buf.Bytes())
	} else {
		_, err = fw.ResponseWriter.Write(fw.buf.Bytes())
	}
	fw.buf.Reset()
	return err
}

// finish sends whatever the handler left buffered and returns the gzip writer
// to the pool.
func (fw *fastWriter) finish() {
	if !fw.wroteHeader {
		// A handler that wrote nothing at all still answered 200.
		fw.WriteHeader(http.StatusOK)
	}
	if !fw.committed {
		fw.commit(false)
	}
	_ = fw.drain()
	if fw.zw != nil {
		_ = fw.zw.Close()
		fw.zw.Reset(io.Discard)
		gzipWriters.Put(fw.zw)
		fw.zw = nil
	}
}

func bodyAllowed(code int) bool {
	return code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified
}

// writeJSON is jsonResponse for a fastWriter: it encodes before the headers go
// out, so the encode shows up in Server-Timing and the size is known when the
// gzip decision is made.
func writeJSON(fw *fastWriter, status int, data interface{}) {
	start := time.Now()
	body, err := json.Marshal(data)
	fw.timing.since("encode", start)
	if err != nil {
		fw.Header().Set("Content-Type", "application/json")
		fw.WriteHeader(http.StatusInternalServerError)
		_, _ = fw.Write([]byte(`{"error":"Internal Server Error","message":"encode failed"}` + "\n"))
		return
	}
	body = append(body, '\n')
	fw.Header().Set("Content-Type", "application/json")
	fw.Header().Set("Content-Length", strconv.Itoa(len(body)))
	fw.WriteHeader(status)
	_, _ = fw.Write(body)
}
