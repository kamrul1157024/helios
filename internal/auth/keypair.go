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

func (k *Keypair) PrivateKeyBase64() string {
	// Ed25519 private key is 64 bytes (seed + public), we only need the 32-byte seed
	return base64.RawURLEncoding.EncodeToString(k.PrivateKey.Seed())
}

func (k *Keypair) SetupPayload() string {
	return fmt.Sprintf("helios://setup?key=%s", k.PrivateKeyBase64())
}
