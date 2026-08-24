package server

import "fmt"

// Show implements mcp.Notifier. It relays a view change an agent asked for and
// reports how many clients received it.
//
// The count is connected clients, not people looking at this session. The event
// stream has one connection per host, not per session, so it cannot say who is
// reading what. Zero is the answer that matters: it tells the agent nobody is
// watching, so it should write prose instead of pointing at a screen.
func (sh *Shared) Show(payload map[string]interface{}) int {
	clients := sh.SSE.ClientCount()
	if clients == 0 {
		return 0
	}
	sh.SSE.Broadcast(SSEEvent{Type: "show", Data: payload})
	return clients
}

// Root implements mcp.Review: the repository a session sits in. Deck-free —
// the agent names files absolutely and this only anchors the review.
func (sh *Shared) Root(sessionID string) (string, error) {
	sess, err := sh.DB.GetSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("look up session %s: %w", sessionID, err)
	}
	if sess == nil {
		return "", fmt.Errorf("no session %s", sessionID)
	}
	root, err := gitRepoRoot(sess.CWD)
	if err != nil {
		return "", fmt.Errorf("session %s is not in a repository", sessionID)
	}
	return root, nil
}

// Changed implements mcp.Review. Merge base rather than the branch tip, for the
// same reason the review panel uses it: commits landed on the base since the
// branch was cut are not this branch's work, and a two-dot diff reports them as
// changes it undid.
func (sh *Shared) Changed(root, base string) ([]string, error) {
	from := mergeBaseFrom(root, base, "HEAD", map[string][]string{"merge_base": {"true"}})
	files, err := changedFiles(root, from, "HEAD")
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths, nil
}

// Reviewed implements mcp.Review.
func (sh *Shared) Reviewed(root, base string) ([]string, error) {
	return sh.DB.ReviewedFiles(root, base)
}
