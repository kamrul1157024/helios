package server

import (
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// Larger than the 10 MB read limit: a phone photo clears that on its own,
	// and the agent reads what lands here with its own tools rather than
	// through the file API.
	maxUploadSize = 25 * 1024 * 1024
	// Parts above this spill to a temp file instead of being held in memory.
	uploadMemoryLimit = 8 * 1024 * 1024
)

type uploadedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// uploadDir is where a session's attachments live: beside the database and the
// logs, not in the workspace. A repository is the user's, and dropping files
// into it to hand the agent a screenshot would show up in their next diff.
func uploadDir(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helios", "uploads", sessionID), nil
}

// handleSessionUpload takes multipart form files and writes them to the
// session's upload directory, answering with the absolute path of each.
//
// The path is the point: the client puts it in the prompt and the agent opens
// it with Read, so nothing about the bytes has to cross the model's context to
// get there.
func (s *PublicServer) handleSessionUpload(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/files")
	if id == "" {
		jsonError(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(uploadMemoryLimit); err != nil {
		// MaxBytesReader is the usual cause, and a size is more use than a parse
		// error the caller can do nothing with.
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":    "upload_too_large",
			"message":  fmt.Sprintf("upload exceeds the %d MB limit", maxUploadSize/(1024*1024)),
			"max_size": maxUploadSize,
		})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	headers := r.MultipartForm.File["file"]
	if len(headers) == 0 {
		jsonError(w, "no files in request", http.StatusBadRequest)
		return
	}

	dir, err := uploadDir(id)
	if err != nil {
		jsonError(w, "no home directory to store uploads in", http.StatusInternalServerError)
		return
	}
	// 0700: an attachment is as private as the session it belongs to.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		jsonError(w, "failed to create upload directory", http.StatusInternalServerError)
		return
	}

	saved := make([]uploadedFile, 0, len(headers))
	for _, header := range headers {
		file, err := storeUpload(dir, header)
		if err != nil {
			log.Printf("session-upload: session=%s file=%q: %v", id, header.Filename, err)
			jsonError(w, fmt.Sprintf("failed to store %s", safeName(header.Filename)), http.StatusInternalServerError)
			return
		}
		saved = append(saved, file)
	}

	log.Printf("session-upload: session=%s stored %d file(s) in %s", id, len(saved), dir)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"files": saved})
}

func storeUpload(dir string, header *multipart.FileHeader) (uploadedFile, error) {
	src, err := header.Open()
	if err != nil {
		return uploadedFile{}, err
	}
	defer src.Close()

	path := uniquePath(dir, safeName(header.Filename))
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return uploadedFile{}, err
	}
	defer dst.Close()

	size, err := io.Copy(dst, src)
	if err != nil {
		// A half-written attachment is worse than none: the agent would read it
		// and report on a truncated file.
		os.Remove(path)
		return uploadedFile{}, err
	}

	return uploadedFile{Name: filepath.Base(path), Path: path, Size: size}, nil
}

// safeName reduces whatever the client sent to a single filename. The name
// comes from a remote client, so a path in it is either a browser quirk or an
// attempt to write outside the directory; neither is worth honouring.
func safeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimLeft(base, ".")
	if base == "" || base == "/" {
		return "upload"
	}
	// Control characters and separators have no business in a filename, and the
	// path goes on to be pasted into a prompt.
	//
	// Whitespace goes with them: the agent reads the path out of a line of
	// prose, and "Screenshot 2026-08-15 at 2.33.50 PM.png" — what macOS calls
	// every screenshot — is four words the agent cannot tell from the sentence
	// around them.
	base = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, base)
	if len(base) > 120 {
		ext := filepath.Ext(base)
		base = base[:120-len(ext)] + ext
	}
	return base
}

// uniquePath keeps the name the user knows the file by, and only numbers it
// when that name is taken — screenshot.png, screenshot-1.png.
func uniquePath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
