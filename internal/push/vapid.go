package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

type VAPIDKeys struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  string // base64url-encoded uncompressed public key
}

func LoadOrGenerateVAPID(heliosDir string) (*VAPIDKeys, error) {
	keyDir := filepath.Join(heliosDir, "push")
	privPath := filepath.Join(keyDir, "vapid_private.pem")
	pubPath := filepath.Join(keyDir, "vapid_public.txt")

	// Try to load existing keys
	if privData, err := os.ReadFile(privPath); err == nil {
		if pubData, err := os.ReadFile(pubPath); err == nil {
			block, _ := pem.Decode(privData)
			if block != nil {
				key, err := x509.ParseECPrivateKey(block.Bytes)
				if err == nil {
					return &VAPIDKeys{
						PrivateKey: key,
						PublicKey:  string(pubData),
					}, nil
				}
			}
		}
	}

	// Generate new VAPID keys
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate VAPID key: %w", err)
	}

	// Encode public key as uncompressed point (65 bytes) in base64url
	pubBytes := elliptic.Marshal(privateKey.PublicKey.Curve, privateKey.PublicKey.X, privateKey.PublicKey.Y)
	publicKeyB64 := base64.RawURLEncoding.EncodeToString(pubBytes)

	// Save keys
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return nil, fmt.Errorf("create push dir: %w", err)
	}

	privDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return nil, fmt.Errorf("write private key: %w", err)
	}

	if err := os.WriteFile(pubPath, []byte(publicKeyB64), 0644); err != nil {
		return nil, fmt.Errorf("write public key: %w", err)
	}

	return &VAPIDKeys{
		PrivateKey: privateKey,
		PublicKey:  publicKeyB64,
	}, nil
}
