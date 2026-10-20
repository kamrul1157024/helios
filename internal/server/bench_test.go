package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// syntheticPaths builds n paths shaped like a real checkout: a few levels of
// directories, repeated names, and a spread of extensions, so the mask filter
// and the scorer see what they see in the wild rather than n unique strings.
func syntheticPaths(n int) []string {
	tops := []string{"internal", "cmd", "desktop/src", "mobile/lib", "docs/specs", "scripts", "pkg"}
	mids := []string{"server", "provider", "renderer", "components", "widgets", "store", "util", "api"}
	exts := []string{".go", ".ts", ".tsx", ".dart", ".md", ".json", "_test.go"}
	paths := make([]string, 0, n)
	for i := 0; len(paths) < n; i++ {
		paths = append(paths, fmt.Sprintf("%s/%s/m%03d/file_%05d%s",
			tops[i%len(tops)], mids[(i/7)%len(mids)], (i/56)%400, i, exts[i%len(exts)]))
	}
	return paths
}

func BenchmarkRank100k(b *testing.B) {
	entries := newIndexEntries(syntheticPaths(100000))
	for _, q := range []string{"f", "srv", "filesrvg"} {
		b.Run(fmt.Sprintf("q%d", len(q)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rankEntries(entries, q, defaultSearchLimit)
			}
		})
	}
}

func BenchmarkListDir(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 5000; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := listDir(dir); err != nil {
			b.Fatal(err)
		}
	}
}

// benchFile writes size bytes of source-like text, which is what the read
// routes mostly serve and what gzip is measured against.
func benchFile(b *testing.B, size int) string {
	b.Helper()
	line := []byte("\tif err := handler.ServeHTTP(w, r); err != nil { return fmt.Errorf(\"serve: %w\", err) }\n")
	buf := make([]byte, 0, size)
	for len(buf) < size {
		buf = append(buf, line...)
	}
	path := filepath.Join(b.TempDir(), "big.go")
	if err := os.WriteFile(path, buf[:size], 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

func BenchmarkReadJSON(b *testing.B) {
	s := &PublicServer{}
	for _, size := range []int{1 << 20, 10 << 20} {
		path := benchFile(b, size)
		b.Run(fmt.Sprintf("%dMB", size>>20), func(b *testing.B) {
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/file?"+url.Values{"path": {path}}.Encode(), nil)
				rec := httptest.NewRecorder()
				s.handleReadFile(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("status %d", rec.Code)
				}
			}
		})
	}
}

func BenchmarkReadRaw(b *testing.B) {
	s := &PublicServer{}
	handler := fastFilesFunc(s.handleReadRaw)
	for _, size := range []int{1 << 20, 10 << 20} {
		path := benchFile(b, size)
		for _, gz := range []bool{false, true} {
			name := fmt.Sprintf("%dMB/plain", size>>20)
			if gz {
				name = fmt.Sprintf("%dMB/gzip", size>>20)
			}
			b.Run(name, func(b *testing.B) {
				b.SetBytes(int64(size))
				for i := 0; i < b.N; i++ {
					req := httptest.NewRequest(http.MethodGet, "/api/file/raw?"+url.Values{"path": {path}}.Encode(), nil)
					if gz {
						req.Header.Set("Accept-Encoding", "gzip")
					}
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						b.Fatalf("status %d", rec.Code)
					}
				}
			})
		}
	}
}

// BenchmarkIndexPatch measures the in-memory half of a patch: 1000 creates and
// 1000 removes applied to a 100k list. The disk half is one ReadDir per dirty
// directory, which BenchmarkListDir already prices.
func BenchmarkIndexPatch(b *testing.B) {
	entries := sortEntries(newIndexEntries(syntheticPaths(100000)))
	adds := make([]string, 1000)
	removed := make(map[string]bool, 1000)
	for i := range adds {
		adds[i] = fmt.Sprintf("internal/server/new/file_%04d.go", i)
		removed[entries[i*97].rel] = true
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fresh := append([]string(nil), adds...)
		if got := applyDiff(entries, fresh, removed, nil); len(got) != len(entries) {
			b.Fatalf("patched list has %d entries, want %d", len(got), len(entries))
		}
	}
}
