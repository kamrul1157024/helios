package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

type Keypair struct {
	KID        string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func GenerateKeypair(kid string) (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	return &Keypair{
		KID:        kid,
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

func (k *Keypair) PublicKeyBase64() string {
	return base64.RawURLEncoding.EncodeToString(k.PublicKey)
}

// GeneratePairingToken creates a cryptographically random token string.
func GeneratePairingToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate pairing token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
