package server

import (
	"encoding/json"
	"net/http"
)

// handleGetReviewed lists the files already read for a repository and base, so
// the panel can show them ticked after a restart and an agent can skip them.
func (s *PublicServer) handleGetReviewed(w http.ResponseWriter, r *http.Request) {
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := r.URL.Query().Get("base")
	if base == "" {
		jsonError(w, "missing base", http.StatusBadRequest)
		return
	}

	files, err := s.shared.DB.ReviewedFiles(root, base)
	if err != nil {
		jsonError(w, "failed to load reviewed files", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"root": root, "base": base, "files": files})
}

// handleSetReviewed records or clears one file, or clears the whole review.
func (s *PublicServer) handleSetReviewed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		Base     string `json:"base"`
		File     string `json:"file"`
		Reviewed bool   `json:"reviewed"`
		Clear    bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	root, err := gitRepoRoot(req.Path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Base == "" {
		jsonError(w, "missing base", http.StatusBadRequest)
		return
	}

	if req.Clear {
		if err := s.shared.DB.ClearReviewed(root, req.Base); err != nil {
			jsonError(w, "failed to clear", http.StatusInternalServerError)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	if req.File == "" {
		jsonError(w, "missing file", http.StatusBadRequest)
		return
	}
	if err := s.shared.DB.MarkReviewed(root, req.Base, req.File, req.Reviewed); err != nil {
		jsonError(w, "failed to save", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}
