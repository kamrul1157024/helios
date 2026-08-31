package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// A one-pixel GIF: short, and not valid UTF-8, which is the whole point.
var gifBytes = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00,
	0x00, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x21, 0xf9, 0x04, 0x01, 0x00,
	0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileBody(t *testing.T, path string, query url.Values) map[string]interface{} {
	t.Helper()
	query.Set("path", path)
	return getJSON(t, (&PublicServer{}).handleReadFile, query)
}

/*
The contract with every client already installed.

A build that predates the encoding field sends no parameter and decides "this
is binary" by looking for a NUL. It must keep getting exactly what it got
before — U+FFFD substitutions and all — because the alternative is that it
starts printing base64 at the user instead of saying the file is binary.
*/
func TestReadFileWithoutEncodingIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixel.gif")
	writeBytes(t, path, gifBytes)

	body := readFileBody(t, path, url.Values{})

	if _, ok := body["encoding"]; ok {
		t.Fatalf("a client that did not ask must not be told: %v", body["encoding"])
	}

	// What the old handler produced: string(content) through the JSON encoder,
	// which is where the U+FFFD substitution happens. Derived rather than
	// written out, so this stays the definition of "unchanged" rather than a
	// copy of it.
	raw, err := json.Marshal(string(gifBytes))
	if err != nil {
		t.Fatal(err)
	}
	var want string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	content, _ := body["content"].(string)
	if content != want {
		t.Fatalf("content changed shape\n got %q\nwant %q", content, want)
	}
	// And that string is the lossy one. Staying lossy is the behaviour pinned
	// here: the fix is the new parameter, not a quiet change to this path.
	if bytes.Equal([]byte(content), gifBytes) {
		t.Fatal("expected the legacy path to still lose the bytes")
	}
}

func TestReadFileAutoEncodesBinaryAsBase64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixel.gif")
	writeBytes(t, path, gifBytes)

	body := readFileBody(t, path, url.Values{"encoding": {"auto"}})

	if body["encoding"] != "base64" {
		t.Fatalf("encoding = %v, want base64", body["encoding"])
	}
	decoded, err := base64.StdEncoding.DecodeString(body["content"].(string))
	if err != nil {
		t.Fatalf("content did not decode: %v", err)
	}
	if !bytes.Equal(decoded, gifBytes) {
		t.Fatalf("decoded %d bytes, want the %d written", len(decoded), len(gifBytes))
	}
}

// Multi-byte text is still text: no base64, so the editor path pays nothing.
func TestReadFileAutoLeavesTextAlone(t *testing.T) {
	const text = "héllo — 世界\n"
	path := filepath.Join(t.TempDir(), "hello.txt")
	writeBytes(t, path, []byte(text))

	body := readFileBody(t, path, url.Values{"encoding": {"auto"}})

	if body["encoding"] != "utf8" {
		t.Fatalf("encoding = %v, want utf8", body["encoding"])
	}
	if got := body["content"].(string); got != text {
		t.Fatalf("content = %q, want %q", got, text)
	}
}

/*
NUL is valid UTF-8.

Which means text holding one still comes back as utf8, and the clients' older
"does it contain a NUL" heuristic keeps firing underneath the new field rather
than being replaced by it.
*/
func TestReadFileAutoKeepsNulBearingTextAsUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nul.txt")
	writeBytes(t, path, []byte("before\x00after"))

	body := readFileBody(t, path, url.Values{"encoding": {"auto"}})

	if body["encoding"] != "utf8" {
		t.Fatalf("encoding = %v, want utf8", body["encoding"])
	}
	if got := body["content"].(string); got != "before\x00after" {
		t.Fatalf("content = %q", got)
	}
}

func TestReadFileAutoHandlesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	writeBytes(t, path, nil)

	body := readFileBody(t, path, url.Values{"encoding": {"auto"}})

	if body["encoding"] != "utf8" {
		t.Fatalf("encoding = %v, want utf8", body["encoding"])
	}
	if got := body["content"].(string); got != "" {
		t.Fatalf("content = %q", got)
	}
}

// The size limit is about what is on disk, so it answers before anything is
// encoded — a 9 MB image must not be base64'd into memory to be refused.
func TestReadFileRefusesOversizeBeforeEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	writeBytes(t, path, bytes.Repeat([]byte{0xff}, maxFileSize+1))

	req := httptest.NewRequest(http.MethodGet, "/?path="+url.QueryEscape(path)+"&encoding=auto", nil)
	rec := httptest.NewRecorder()
	(&PublicServer{}).handleReadFile(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestEncodeFileContent(t *testing.T) {
	cases := []struct {
		name     string
		content  []byte
		want     string
		encoding string
	}{
		{"no parameter leaves it alone", []byte("hi"), "", ""},
		{"auto marks text", []byte("hi"), "auto", "utf8"},
		{"auto encodes binary", gifBytes, "auto", "base64"},
		{"an unknown parameter is not auto", gifBytes, "utf8", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, encoding := encodeFileContent(c.content, c.want)
			if encoding != c.encoding {
				t.Fatalf("encoding = %q, want %q", encoding, c.encoding)
			}
		})
	}
}
