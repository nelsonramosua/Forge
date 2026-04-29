package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[chan []byte]struct{})}
}

func (h *eventHub) publish(kind string, value interface{}) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	frame := append([]byte("event: "+kind+"\n"+"data: "), body...)
	frame = append(frame, []byte("\n\n")...)

	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (h *eventHub) serve(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		close(ch)
	}()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		case frame := <-ch:
			_, _ = w.Write(frame)
			flusher.Flush()
		}
	}
}
