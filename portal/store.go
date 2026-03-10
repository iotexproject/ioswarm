package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps SQLite operations.
type Store struct {
	db *sql.DB
}

// User represents a registered user.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// APIKey represents a generated API key.
type APIKey struct {
	ID        int64
	UserID    int64
	AgentID   string
	APIKey    string
	Label     string
	Revoked   bool
	CreatedAt time.Time
}

// NewStore opens (or creates) the SQLite database and runs migrations.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id),
			agent_id TEXT UNIQUE NOT NULL,
			api_key TEXT NOT NULL,
			label TEXT DEFAULT '',
			revoked INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(email, passwordHash string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO users (email, password_hash) VALUES (?, ?)", email, passwordHash)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserByEmail looks up a user by email.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateSession stores a session token.
func (s *Store) CreateSession(token string, userID int64) error {
	_, err := s.db.Exec("INSERT INTO sessions (token, user_id) VALUES (?, ?)", token, userID)
	return err
}

// GetSession returns the user ID for a session token.
func (s *Store) GetSession(token string) (int64, error) {
	var userID int64
	err := s.db.QueryRow("SELECT user_id FROM sessions WHERE token = ?", token).Scan(&userID)
	return userID, err
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

// CreateAPIKey inserts a new API key.
func (s *Store) CreateAPIKey(userID int64, agentID, apiKey, label string) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO api_keys (user_id, agent_id, api_key, label) VALUES (?, ?, ?, ?)",
		userID, agentID, apiKey, label,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAPIKeys returns all keys (including revoked) for a user.
func (s *Store) ListAPIKeys(userID int64) ([]APIKey, error) {
	rows, err := s.db.Query(
		"SELECT id, user_id, agent_id, api_key, label, revoked, created_at FROM api_keys WHERE user_id = ? ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.AgentID, &k.APIKey, &k.Label, &k.Revoked, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeAPIKey soft-deletes an API key.
func (s *Store) RevokeAPIKey(userID, keyID int64) error {
	_, err := s.db.Exec("UPDATE api_keys SET revoked = 1 WHERE id = ? AND user_id = ?", keyID, userID)
	return err
}

// CountActiveKeys returns count of non-revoked keys for a user.
func (s *Store) CountActiveKeys(userID int64) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_id = ? AND revoked = 0", userID).Scan(&count)
	return count, err
}
