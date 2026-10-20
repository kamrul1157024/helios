package desktop

import (
	"testing"
	"time"
)

func TestTicketWorksOnce(t *testing.T) {
	tickets := NewTickets()
	value, expires, err := tickets.Issue("phone")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(expires); d <= 0 || d > TicketTTL {
		t.Fatalf("expires in %s, want within %s", d, TicketTTL)
	}
	device, ok := tickets.Redeem(value)
	if !ok || device != "phone" {
		t.Fatalf("first Redeem = %q, %v", device, ok)
	}
	if _, ok := tickets.Redeem(value); ok {
		t.Fatal("a used ticket worked again")
	}
}

func TestTicketExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tickets := NewTickets()
	tickets.now = func() time.Time { return now }
	value, _, err := tickets.Issue("phone")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(TicketTTL)
	if _, ok := tickets.Redeem(value); ok {
		t.Fatal("an expired ticket worked")
	}
}

func TestUnknownTicketFails(t *testing.T) {
	if _, ok := NewTickets().Redeem("nope"); ok {
		t.Fatal("an unknown ticket worked")
	}
	if _, ok := NewTickets().Redeem(""); ok {
		t.Fatal("an empty ticket worked")
	}
}

func TestSweepDropsExpiredTickets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tickets := NewTickets()
	tickets.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, _, err := tickets.Issue("phone"); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * TicketTTL)
	if _, _, err := tickets.Issue("phone"); err != nil {
		t.Fatal(err)
	}
	if n := len(tickets.issued); n != 1 {
		t.Fatalf("%d tickets kept, want 1", n)
	}
}
