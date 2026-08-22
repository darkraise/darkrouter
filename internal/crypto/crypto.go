// Package crypto derives the credential-encryption key from the master key and
// seals individual credentials with it.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// DefaultIterations is deliberately high. The master key may be a human
	// passphrase — nothing forces it to be 32 random bytes — and a low count
	// makes offline attack of a stolen database file cheap. It runs once at
	// startup and once per rotation, so the cost is invisible in operation.
	//
	// The count is stored alongside the salt so it can be raised later without
	// breaking existing databases.
	DefaultIterations = 600000

	// SaltBytes is 16, the size RFC 8018 recommends as a minimum.
	SaltBytes = 16

	keyBytes = 32 // AES-256
)

// Key is a derived encryption key. Derive it once at startup and reuse it;
// deriving per credential would run PBKDF2 on the request path.
type Key struct {
	aead cipher.AEAD
}

// DeriveKey stretches master into an AES-256-GCM key.
func DeriveKey(master string, salt []byte, iterations int) (*Key, error) {
	if master == "" {
		return nil, errors.New("crypto: master key is empty")
	}
	if len(salt) == 0 {
		return nil, errors.New("crypto: salt is empty")
	}
	if iterations < 1 {
		return nil, fmt.Errorf("crypto: iteration count %d is not positive", iterations)
	}
	raw, err := pbkdf2.Key(sha256.New, master, salt, iterations, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("crypto: derive: %w", err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return &Key{aead: aead}, nil
}

// Seal encrypts plaintext under a fresh random nonce, binding the result to
// aad. Callers pass the credential row id as aad, which is what stops this
// ciphertext being moved to another row undetected.
//
// The nonce is returned rather than prepended so it lands in its own column and
// stays legible when someone inspects the database by hand.
func (k *Key) Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return k.aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

// Open reverses Seal. A failure here means the master key is wrong, the row was
// tampered with, or a ciphertext was swapped between rows; the three are
// indistinguishable by design, so the message stays generic.
func (k *Key) Open(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != k.aead.NonceSize() {
		return nil, fmt.Errorf("crypto: nonce is %d bytes, want %d", len(nonce), k.aead.NonceSize())
	}
	out, err := k.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errors.New("crypto: authentication failed")
	}
	return out, nil
}

// NewSalt generates a per-database salt. It is stored in settings on first run.
func NewSalt() ([]byte, error) {
	s := make([]byte, SaltBytes)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("crypto: salt: %w", err)
	}
	return s, nil
}
