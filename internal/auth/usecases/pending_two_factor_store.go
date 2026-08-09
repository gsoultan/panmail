package usecases

import (
	"sync"
	"time"
)

// pendingEnrolmentTTL is how long a generated TOTP secret stays claimable
// before the user has to restart enrolment.
const pendingEnrolmentTTL = 10 * time.Minute

type pendingEnrolment struct {
	secret    string
	expiresAt time.Time
}

// pendingTwoFactorStore holds TOTP secrets that have been generated for a user
// but not yet confirmed. Secrets live here, server-side, and are keyed by user
// id — they are never accepted back from the client, which is what stops a
// caller from presenting a secret they control and having it treated as the
// user's own.
//
// It is safe for concurrent use.
type pendingTwoFactorStore struct {
	mu      sync.Mutex
	entries map[string]pendingEnrolment
}

func newPendingTwoFactorStore() *pendingTwoFactorStore {
	return &pendingTwoFactorStore{entries: make(map[string]pendingEnrolment)}
}

// Put records a freshly generated secret for the user, replacing any previous
// pending enrolment.
func (s *pendingTwoFactorStore) Put(userID, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictExpiredLocked()
	s.entries[userID] = pendingEnrolment{
		secret:    secret,
		expiresAt: time.Now().Add(pendingEnrolmentTTL),
	}
}

// Get returns the user's pending secret if one is outstanding and unexpired.
func (s *pendingTwoFactorStore) Get(userID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[userID]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.entries, userID)
		return "", false
	}
	return entry.secret, true
}

// Delete drops the user's pending enrolment, called once it is confirmed.
func (s *pendingTwoFactorStore) Delete(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, userID)
}

func (s *pendingTwoFactorStore) evictExpiredLocked() {
	now := time.Now()
	for userID, entry := range s.entries {
		if now.After(entry.expiresAt) {
			delete(s.entries, userID)
		}
	}
}
