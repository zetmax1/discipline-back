// Package calendarcrypt keeps secret calendar URLs encrypted at rest (F9a §4.4): AES-256-GCM with
// CALENDAR_URL_KEY, ciphertext = "v1" key id ‖ 12-byte nonce ‖ sealed URL, so a later key rotation
// can re-encrypt. A fingerprint (HMAC-SHA256 with a key derived from the same secret) finds a
// duplicate source without decrypting.
package calendarcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
)

const keyID = "v1"

// Box seals and opens URLs.
type Box struct {
	aead   cipher.AEAD
	macKey []byte
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("calendar URL key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("calendar-url-fingerprint"))
	return &Box{aead: aead, macKey: mac.Sum(nil)}, nil
}

// Seal encrypts the URL.
func (b *Box) Seal(u string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(keyID), nonce...)
	return b.aead.Seal(out, nonce, []byte(u), []byte(keyID)), nil
}

// Open decrypts a sealed URL.
func (b *Box) Open(sealed []byte) (string, error) {
	n := len(keyID) + b.aead.NonceSize()
	if len(sealed) < n || string(sealed[:len(keyID)]) != keyID {
		return "", errors.New("unknown calendar URL ciphertext")
	}
	plain, err := b.aead.Open(nil, sealed[len(keyID):n], sealed[n:], []byte(keyID))
	if err != nil {
		return "", errors.New("calendar URL ciphertext does not open")
	}
	return string(plain), nil
}

// Fingerprint is HMAC-SHA256 of the normalised URL (lower-case scheme and host).
func (b *Box) Fingerprint(raw string) []byte {
	norm := raw
	if u, err := url.Parse(raw); err == nil {
		u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
		norm = u.String()
	}
	mac := hmac.New(sha256.New, b.macKey)
	mac.Write([]byte(norm))
	return mac.Sum(nil)
}
