package push

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/kamrul1157024/helios/internal/store"
)

type Sender struct {
	db    *store.Store
	vapid *VAPIDKeys
}

func NewSender(db *store.Store, vapid *VAPIDKeys) *Sender {
	return &Sender{db: db, vapid: vapid}
}

func (s *Sender) VAPIDPublicKey() string {
	return s.vapid.PublicKey
}

type PushPayload struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Body    string            `json:"body"`
	URL     string            `json:"url,omitempty"`
	Actions []PushAction      `json:"actions,omitempty"`
}

type PushAction struct {
	Action string `json:"action"`
	Title  string `json:"title"`
}

func (s *Sender) SendToAll(payload PushPayload) {
	subs, err := s.db.ListPushSubscriptions()
	if err != nil {
		log.Printf("push: failed to list subscriptions: %v", err)
		return
	}

	if len(subs) == 0 {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("push: failed to marshal payload: %v", err)
		return
	}

	for _, sub := range subs {
		go s.sendToSubscription(sub, data)
	}
}

func (s *Sender) sendToSubscription(sub store.PushSubscription, data []byte) {
	wSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}

	resp, err := webpush.SendNotification(data, wSub, &webpush.Options{
		Subscriber:      "helios@localhost",
		VAPIDPublicKey:  s.vapid.PublicKey,
		VAPIDPrivateKey: ecdsaPrivateKeyToBase64(s.vapid.PrivateKey),
		TTL:             300,
		Urgency:         webpush.UrgencyHigh,
	})

	if err != nil {
		log.Printf("push: send failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 410 || resp.StatusCode == 404 {
		log.Printf("push: subscription expired, removing")
		s.db.DeletePushSubscription(sub.Endpoint)
	} else if resp.StatusCode >= 400 {
		log.Printf("push: send returned status %d", resp.StatusCode)
	}
}

// ecdsaPrivateKeyToBase64 encodes the ECDSA private key's D value as base64url (no padding).
// webpush-go expects this format for VAPIDPrivateKey.
func ecdsaPrivateKeyToBase64(key *ecdsa.PrivateKey) string {
	d := key.D.Bytes()
	// Pad to 32 bytes if needed
	padded := make([]byte, 32)
	copy(padded[32-len(d):], d)
	return base64.RawURLEncoding.EncodeToString(padded)
}
