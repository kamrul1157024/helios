package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// notifyGroups tells every client the grouping changed. session_updated rather
// than an event of its own: the ranks ride on the session list, so a client
// that refetches sessions has already picked them up.
func (s *PublicServer) notifyGroups() {
	s.shared.SSE.Broadcast(SSEEvent{
		Type: "session_updated",
		Data: map[string]interface{}{"groups": true},
	})
}

func (s *PublicServer) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.shared.DB.ListGroups()
	if err != nil {
		log.Printf("groups: list: %v", err)
		jsonError(w, "failed to list groups", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

func (s *PublicServer) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	group, err := s.shared.DB.CreateGroup(req.Name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.notifyGroups()
	jsonResponse(w, http.StatusOK, group)
}

// handleSetGroupOrder writes a hand-arranged order. The whole list at once, as
// the session order route does: dragging one header shifts every header it
// passed, so the client already knows the arrangement it wants.
func (s *PublicServer) handleSetGroupOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Order) == 0 {
		jsonError(w, "missing order", http.StatusBadRequest)
		return
	}

	if err := s.shared.DB.SetGroupOrder(req.Order); err != nil {
		log.Printf("groups: order: %v", err)
		jsonError(w, "failed to save order", http.StatusInternalServerError)
		return
	}
	s.notifyGroups()
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleGroup serves PATCH and DELETE on one group. Routed together because
// both are "/api/groups/{key}" and net/http's mux would otherwise need the key
// pattern written twice.
func (s *PublicServer) handleGroup(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/groups/")
	if key == "" || strings.Contains(key, "/") {
		jsonError(w, "missing group key", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := s.shared.DB.RenameGroup(key, req.Name); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	case http.MethodDelete:
		// The sessions that held it are cleared in the same transaction, so
		// none is left pointing at a group that is gone.
		if err := s.shared.DB.DeleteGroup(key); err != nil {
			log.Printf("groups: delete %s: %v", key, err)
			jsonError(w, "failed to delete group", http.StatusInternalServerError)
			return
		}
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.notifyGroups()
	jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}
