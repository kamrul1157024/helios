package server

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/store"
)

type contextKey string

const deviceKIDKey contextKey = "device_kid"

const cookieName = "helios_token"

// cookieAuthMiddleware reads JWT from the helios_token cookie.
func cookieAuthMiddleware(db *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, `{"error":"unauthorized","message":"missing auth cookie"}`, http.StatusUnauthorized)
				return
			}

			kid, err := auth.ValidateJWT(cookie.Value, func(kid string) (ed25519.PublicKey, error) {
				device, err := db.GetActiveDevice(kid)
				if err != nil {
					return nil, err
				}
				if device == nil {
					return nil, fmt.Errorf("device not found or revoked")
				}
				return auth.PublicKeyFromBase64(device.PublicKey)
			})

			if err != nil {
				http.Error(w, `{"error":"unauthorized","message":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			db.UpdateDeviceLastSeen(kid)

			ctx := context.WithValue(r.Context(), deviceKIDKey, kid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// frontendAuthMiddleware protects page loads with cookie auth.
// If no valid cookie, redirects to setup page (which is part of the SPA).
func frontendAuthMiddleware(db *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always serve static assets (JS, CSS, icons, manifest, sw.js)
		path := r.URL.Path
		if isStaticAsset(path) {
			next.ServeHTTP(w, r)
			return
		}

		// Check cookie
		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			// No cookie — serve the SPA anyway (it will show setup page)
			next.ServeHTTP(w, r)
			return
		}

		_, err = auth.ValidateJWT(cookie.Value, func(kid string) (ed25519.PublicKey, error) {
			device, err := db.GetActiveDevice(kid)
			if err != nil {
				return nil, err
			}
			if device == nil {
				return nil, fmt.Errorf("device not found or revoked")
			}
			return auth.PublicKeyFromBase64(device.PublicKey)
		})

		if err != nil {
			// Invalid cookie — serve SPA (will show setup)
			next.ServeHTTP(w, r)
			return
		}

		// Valid cookie — serve normally
		next.ServeHTTP(w, r)
	})
}

func isStaticAsset(path string) bool {
	for _, ext := range []string{".js", ".css", ".svg", ".png", ".ico", ".json", ".webmanifest"} {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return path == "/sw.js" || path == "/manifest.json"
}
