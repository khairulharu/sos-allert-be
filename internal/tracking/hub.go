package tracking

import "sync"

// Hub is a transport-agnostic pub/sub for live incident tracking.
// The WebSocket handler subscribes per alert; the SOS use case publishes
// LOCATION_UPDATE payloads. Subscribers get a buffered channel and must
// call the returned unsubscribe function when done.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan []byte]struct{})}
}

// Subscribe returns a channel receiving payloads for alertID.
func (h *Hub) Subscribe(alertID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	if h.subs[alertID] == nil {
		h.subs[alertID] = make(map[chan []byte]struct{})
	}
	h.subs[alertID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if m, ok := h.subs[alertID]; ok {
			delete(m, ch)
			close(ch)
			if len(m) == 0 {
				delete(h.subs, alertID)
			}
		}
	}
}

// Publish delivers payload to all subscribers of alertID. Slow
// subscribers drop the message rather than blocking the sender.
func (h *Hub) Publish(alertID string, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[alertID] {
		select {
		case ch <- payload:
		default:
		}
	}
}

// SubscriberCount is for tests and diagnostics.
func (h *Hub) SubscriberCount(alertID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[alertID])
}
