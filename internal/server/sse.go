package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type sseClient struct {
	ch     chan SSEEvent
	done   chan struct{}
}

type SSEBroadcaster struct {
	mu      sync.RWMutex
	clients map[*sseClient]bool
}

func NewSSEBroadcaster() *SSEBroadcaster {
	return &SSEBroadcaster{
		clients: make(map[*sseClient]bool),
	}
}

func (b *SSEBroadcaster) Broadcast(event SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for client := range b.clients {
		select {
		case client.ch <- event:
		default:
			// Client buffer full, skip
		}
	}
}

func (b *SSEBroadcaster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	client := &sseClient{
		ch:   make(chan SSEEvent, 64),
		done: make(chan struct{}),
	}

	b.mu.Lock()
	b.clients[client] = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.clients, client)
		b.mu.Unlock()
		close(client.done)
	}()

	// Send initial heartbeat
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-client.ch:
			data, _ := json.Marshal(event.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (b *SSEBroadcaster) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}
