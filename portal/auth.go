package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

const apiKeyPrefix = "iosw_"

// DeriveAgentToken generates an API key using HMAC-SHA256(masterSecret, agentID).
func DeriveAgentToken(masterSecret, agentID string) string {
	mac := hmac.New(sha256.New, []byte(masterSecret))
	mac.Write([]byte(agentID))
	return apiKeyPrefix + hex.EncodeToString(mac.Sum(nil))
}

// HashPassword hashes a password with bcrypt.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword checks a password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateSessionToken creates a random 32-byte hex session token.
func GenerateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// GenerateAgentID creates a random agent ID like "agent-a1b2c3d4".
func GenerateAgentID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("agent-%s", hex.EncodeToString(b))
}

// sessionMiddleware wraps a handler to require a valid session cookie.
// Injects userID into the request context.
func (app *App) sessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		userID, err := app.store.GetSession(cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r = r.WithContext(setUserID(r.Context(), userID))
		next(w, r)
	}
}
