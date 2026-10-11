package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// MagicToken represents a short-lived access token for passwordless dashboard authentication.
type MagicToken struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UsedCount int       `json:"used_count"`
}

// IsExpired reports whether the token has expired at the given time.
func (t *MagicToken) IsExpired(now time.Time) bool {
	if t == nil {
		return true
	}
	return now.After(t.ExpiresAt)
}

// RemainingTTL calculates the remaining duration until expiration.
func (t *MagicToken) RemainingTTL(now time.Time) time.Duration {
	if t == nil || t.IsExpired(now) {
		return 0
	}
	return t.ExpiresAt.Sub(now)
}

// TokenManager manages issuance, verification, and lifecycle of short-lived dashboard tokens.
type TokenManager struct {
	mu         sync.RWMutex
	tokens     map[string]*MagicToken
	defaultTTL time.Duration
}

// NewTokenManager initializes a new TokenManager with a default token TTL (e.g. 2 hours).
func NewTokenManager(defaultTTL time.Duration) *TokenManager {
	if defaultTTL <= 0 {
		defaultTTL = 2 * time.Hour
	}
	return &TokenManager{
		tokens:     make(map[string]*MagicToken),
		defaultTTL: defaultTTL,
	}
}

// Generate creates and records a new magic token for a specified user.
func (m *TokenManager) Generate(userID, userName string, customTTL time.Duration) *MagicToken {
	ttl := m.defaultTTL
	if customTTL > 0 {
		ttl = customTTL
	}

	b := make([]byte, 20)
	_, _ = rand.Read(b)
	tokenStr := "twc_m_" + hex.EncodeToString(b)

	now := time.Now()
	entry := &MagicToken{
		Token:     tokenStr,
		UserID:    strings.TrimSpace(userID),
		UserName:  strings.TrimSpace(userName),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanExpiredLocked(now)
	m.tokens[tokenStr] = entry
	return entry
}

// Validate checks whether a magic token exists and has not yet expired.
// If valid, it increments UsedCount and returns the entry and remaining duration.
func (m *TokenManager) Validate(tokenStr string) (*MagicToken, time.Duration, bool) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return nil, 0, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.tokens[tokenStr]
	if !exists {
		return nil, 0, false
	}

	now := time.Now()
	if entry.IsExpired(now) {
		delete(m.tokens, tokenStr)
		return nil, 0, false
	}

	entry.UsedCount++
	return entry, entry.RemainingTTL(now), true
}

// IsValid reports true if the token is known and not expired without mutating used count.
func (m *TokenManager) IsValid(tokenStr string) bool {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, exists := m.tokens[tokenStr]
	if !exists {
		return false
	}
	return !entry.IsExpired(time.Now())
}

// Revoke invalidates an active magic token immediately.
func (m *TokenManager) Revoke(tokenStr string) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, tokenStr)
}

// ActiveTokens returns a snapshot list of currently valid magic tokens.
func (m *TokenManager) ActiveTokens() []*MagicToken {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var list []*MagicToken
	for _, entry := range m.tokens {
		if !entry.IsExpired(now) {
			list = append(list, entry)
		}
	}
	return list
}

func (m *TokenManager) cleanExpiredLocked(now time.Time) {
	for k, entry := range m.tokens {
		if entry.IsExpired(now) {
			delete(m.tokens, k)
		}
	}
}
