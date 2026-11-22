package calendarcrypt

import (
	"bytes"
	"strings"
	"testing"
)

var key = bytes.Repeat([]byte{7}, 32)

func TestSealOpenRoundTripWithKeyIDAndFreshNonce(t *testing.T) {
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://calendar.google.com/calendar/ical/x%40group.calendar.google.com/private-abc/basic.ics"
	c1, err := b.Seal(url)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := b.Seal(url)
	if bytes.Equal(c1, c2) {
		t.Fatal("each seal uses a fresh nonce")
	}
	if !bytes.HasPrefix(c1, []byte("v1")) || bytes.Contains(c1, []byte("private-abc")) {
		t.Fatal("ciphertext starts with the key id and never holds the URL in clear")
	}
	got, err := b.Open(c1)
	if err != nil || got != url {
		t.Fatalf("open: %q %v", got, err)
	}
	c1[len(c1)-1] ^= 1
	if _, err := b.Open(c1); err == nil {
		t.Fatal("a tampered ciphertext must not open")
	}
}

func TestFingerprintFindsTheSameURLWithoutDecrypting(t *testing.T) {
	b, _ := New(key)
	a := b.Fingerprint("https://Calendar.Google.com/cal.ics")
	if !bytes.Equal(a, b.Fingerprint("https://calendar.google.com/cal.ics")) {
		t.Fatal("the host is case-insensitive")
	}
	if bytes.Equal(a, b.Fingerprint("https://calendar.google.com/other.ics")) {
		t.Fatal("different URLs differ")
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if bytes.Equal(a, other.Fingerprint("https://calendar.google.com/cal.ics")) {
		t.Fatal("the fingerprint depends on the secret")
	}
}

func TestNewRejectsAWrongKeySize(t *testing.T) {
	if _, err := New([]byte("short")); err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("expected an error that does not echo the key, got %v", err)
	}
}
