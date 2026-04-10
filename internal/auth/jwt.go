package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func ValidateJWT(tokenString string, getPublicKey func(kid string) (ed25519.PublicKey, error)) (string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, fmt.Errorf("missing kid in token header")
		}

		pubKey, err := getPublicKey(kid)
		if err != nil {
			return nil, err
		}

		return pubKey, nil
	})
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	if !token.Valid {
		return "", fmt.Errorf("token validation failed")
	}

	kid, _ := token.Header["kid"].(string)
	return kid, nil
}

func PublicKeyFromBase64(encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: %d", len(decoded))
	}
	return ed25519.PublicKey(decoded), nil
}

// CreateTestJWT creates a JWT for testing purposes (used by helios auth verify flow)
func CreateTestJWT(privateKey ed25519.PrivateKey, kid string) (string, error) {
	token := jwt.NewWithClaims(&jwt.SigningMethodEd25519{}, jwt.MapClaims{
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
		"sub": "helios-client",
	})
	token.Header["kid"] = kid

	return token.SignedString(privateKey)
}
