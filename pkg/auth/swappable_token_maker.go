package auth

import (
	"sync"
)

type SwappableTokenMaker struct {
	maker TokenMaker
	mu    sync.RWMutex
}

func NewSwappableTokenMaker(maker TokenMaker) *SwappableTokenMaker {
	return &SwappableTokenMaker{maker: maker}
}

func (s *SwappableTokenMaker) SetMaker(maker TokenMaker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maker = maker
}

func (s *SwappableTokenMaker) CreateToken(req TokenRequest) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.maker == nil {
		return "", ErrInvalidToken
	}
	return s.maker.CreateToken(req)
}

func (s *SwappableTokenMaker) VerifyToken(token string, purpose TokenPurpose) (*TokenPayload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.maker == nil {
		return nil, ErrInvalidToken
	}
	return s.maker.VerifyToken(token, purpose)
}
