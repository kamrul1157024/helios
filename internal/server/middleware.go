package server

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/store"
)

type contextKey string

const deviceKIDKey contextKey = "device_kid"

// bearerAuthMiddleware validates an Ed25519-signed JWT from the Authorization
// header. Requires the device to have "active" status.
func bearerAuthMiddleware(db *store.Store) func(http.Handler) http.Handler {
	return jwtBearerMiddleware(db, false)
}

// pendingOrActiveBearerMiddleware is like bearerAuthMiddleware but also
// accepts devices in "pending" status (used during pairing approval polling).
func pendingOrActiveBearerMiddleware(db *store.Store) func(http.Handler) http.Handler {
	return jwtBearerMiddleware(db, true)
}

func jwtBearerMiddleware(db *store.Store, allowPending bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"unauthorized","message":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			kid, err := auth.ValidateJWT(tokenString, func(kid string) (ed25519.PublicKey, error) {
				var device *store.Device
				var lookupErr error
				if allowPending {
					device, lookupErr = db.GetPendingOrActiveDevice(kid)
				} else {
					device, lookupErr = db.GetActiveDevice(kid)
				}
				if lookupErr != nil {
					return nil, lookupErr
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
