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

// Unsubscribe is often deferred and also called explicitly by the WS
// handler; a second call must not close the channel again (panic).
func TestHubUnsubscribeTwiceDoesNotPanic(t *testing.T) {
	h := NewHub()
	_, unsub := h.Subscribe("alt_1")
	// A second subscriber keeps the alert entry alive, which is the
	// case where the double close used to happen.
	_, unsub2 := h.Subscribe("alt_1")
	defer unsub2()

	unsub()
	unsub()
	unsub()

	if n := h.SubscriberCount("alt_1"); n != 1 {
		t.Fatalf("subscriber count = %d, want 1", n)
	}
	// The hub stays usable for a fresh subscription on the same alert.
	_, unsub3 := h.Subscribe("alt_1")
	defer unsub3()
	if n := h.SubscriberCount("alt_1"); n != 2 {
		t.Fatalf("subscriber count = %d, want 2", n)
	}
}
