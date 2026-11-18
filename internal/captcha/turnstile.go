// Package captcha verifies human-check tokens (Cloudflare Turnstile).
package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Verifier checks a captcha token produced by the browser widget. A false result
// with a nil error means the token is invalid; an error means the check could not
// be made, and callers must refuse the request (fail closed).
type Verifier interface {
	Verify(ctx context.Context, token string) (bool, error)
}

// Turnstile verifies tokens with Cloudflare's siteverify endpoint.
type Turnstile struct {
	secret    string
	verifyURL string
	client    *http.Client
}

// NewTurnstile creates a verifier for TURNSTILE_SECRET.
func NewTurnstile(secret string) *Turnstile {
	return &Turnstile{
		secret:    secret,
		verifyURL: "https://challenges.cloudflare.com/turnstile/v0/siteverify",
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Verify reports whether Cloudflare accepts token. Tokens are single-use.
func (t *Turnstile) Verify(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 2048 { // Cloudflare tokens are at most 2048 characters
		return false, nil
	}
	form := url.Values{"secret": {t.secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("turnstile siteverify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("turnstile siteverify: status %d", resp.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return false, fmt.Errorf("turnstile siteverify: %w", err)
	}
	return out.Success, nil
}
