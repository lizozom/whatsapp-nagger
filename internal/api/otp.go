package api

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"
)

type otpEntry struct {
	code      string
	expiresAt time.Time
	attempts  int
	sentAt    time.Time
}

// OTPStore is a thread-safe in-memory store for one-time passwords.
// Entries auto-expire after ttl. No persistence — OTPs are ephemeral.
type OTPStore struct {
	mu      sync.Mutex
	entries map[string]*otpEntry // keyed by phone
	ttl     time.Duration
}

// NewOTPStore creates a store with the given TTL and starts a background
// cleanup goroutine.
func NewOTPStore(ttl time.Duration) *OTPStore {
	s := &OTPStore{
		entries: make(map[string]*otpEntry),
		ttl:     ttl,
	}
	go s.cleanup()
	return s
}

// Generate creates a 6-digit OTP for the given phone number.
// Rate-limited to 1 per phone per 60 seconds.
// Returns the code and any error.
func (s *OTPStore) Generate(phone string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[phone]; ok {
		if time.Since(e.sentAt) < 60*time.Second {
			return "", fmt.Errorf("OTP already sent, wait %d seconds",
				60-int(time.Since(e.sentAt).Seconds()))
		}
	}

	code, err := generateCode()
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}

	s.entries[phone] = &otpEntry{
		code:      code,
		expiresAt: time.Now().Add(s.ttl),
		attempts:  0,
		sentAt:    time.Now(),
	}
	return code, nil
}

// Verify checks the OTP for the given phone. Returns true if valid.
// Max 3 attempts per code. Consumed on success.
func (s *OTPStore) Verify(phone, code string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[phone]
	if !ok {
		return false, fmt.Errorf("no OTP pending for this phone")
	}
	if time.Now().After(e.expiresAt) {
		delete(s.entries, phone)
		return false, fmt.Errorf("OTP expired")
	}
	e.attempts++
	if e.attempts > 3 {
		delete(s.entries, phone)
		return false, fmt.Errorf("too many attempts")
	}
	if e.code != code {
		return false, nil
	}
	// Success — consume the OTP.
	delete(s.entries, phone)
	return true, nil
}

func (s *OTPStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for phone, e := range s.entries {
			if now.After(e.expiresAt) {
				delete(s.entries, phone)
			}
		}
		s.mu.Unlock()
	}
}

func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
