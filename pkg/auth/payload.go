package auth

import (
	"time"
)

// TokenPurpose records what a token is allowed to do. A token minted for one
// purpose is rejected everywhere another purpose is expected, so a short-lived
// credential (such as a pending two-factor challenge) can never be replayed as
// a full API session.
type TokenPurpose string

const (
	// PurposeSession is a fully authenticated API session.
	PurposeSession TokenPurpose = "session"

	// PurposeTwoFactorChallenge proves that a password was verified and that the
	// second factor is still outstanding. It carries no API authority.
	PurposeTwoFactorChallenge TokenPurpose = "two_factor_challenge"
)

type TokenPayload struct {
	UserID    string       `json:"user_id"`
	TenantID  string       `json:"tenant_id"`
	Role      string       `json:"role"`
	Purpose   TokenPurpose `json:"purpose"`
	IssuedAt  time.Time    `json:"issued_at"`
	ExpiredAt time.Time    `json:"expired_at"`
}

// Valid reports whether the payload may be used for the given purpose. Tokens
// with no recorded purpose are rejected: they predate purpose scoping and
// cannot be proven to have been issued for a session.
func (p *TokenPayload) Valid(purpose TokenPurpose) error {
	if time.Now().After(p.ExpiredAt) {
		return ErrExpiredToken
	}
	if p.Purpose != purpose {
		return ErrWrongTokenPurpose
	}
	return nil
}
