package server

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/store"
)

type contextKey string

const deviceKIDKey contextKey = "device_kid"

func authMiddleware(db *store.Store, authEnabled, skipLocal bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if disabled globally
			if !authEnabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for localhost if configured
			if skipLocal && isLocalhost(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract Bearer token
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"unauthorized","message":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			kid, err := auth.ValidateJWT(tokenString, func(kid string) (ed25519.PublicKey, error) {
				device, err := db.GetActiveDevice(kid)
				if err != nil {
					return nil, err
				}
				if device == nil {
					return nil, fmt.Errorf("device not found or revoked")
				}

				pubKey, err := auth.PublicKeyFromBase64(device.PublicKey)
				if err != nil {
					return nil, err
				}

				return pubKey, nil
			})

			if err != nil {
				http.Error(w, `{"error":"unauthorized","message":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			// Update last seen
			db.UpdateDeviceLastSeen(kid)

			// Add device KID to request context
			ctx := context.WithValue(r.Context(), deviceKIDKey, kid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isLocalhost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
