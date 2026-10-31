package tracking

import (
	"testing"
	"time"
)

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("alt_1")
	defer unsub()

	h.Publish("alt_1", []byte(`{"type":"LOCATION_UPDATE"}`))
	select {
	case got := <-ch:
		if string(got) != `{"type":"LOCATION_UPDATE"}` {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}

	// Other alerts do not leak.
	h.Publish("alt_2", []byte("x"))
	select {
	case <-ch:
		t.Fatal("received message for another alert")
	case <-time.After(50 * time.Millisecond):
	}

	unsub()
	if n := h.SubscriberCount("alt_1"); n != 0 {
		t.Fatalf("subscriber count = %d, want 0", n)
	}
}
