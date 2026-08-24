package server

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
