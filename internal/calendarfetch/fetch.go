// Package calendarfetch reads a calendar from a user-supplied secret iCal URL without letting the
// API be used to reach internal or special addresses (F9a §4.1, SSRF). The address is checked in the
// dialer — on the actual remote IP of every connection, redirects included — not only before.
package calendarfetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/idna"
)

// Outcomes; each maps to one API error code and one calendar_sources.last_status.
var (
	ErrInvalid  = errors.New("calendar URL is not a secret https/webcal address") // ics_url_invalid / invalid
	ErrBlocked  = errors.New("calendar URL points somewhere we never fetch")       // ics_url_blocked / blocked
	ErrFailed   = errors.New("calendar could not be fetched")                      // ics_fetch_failed / failed
	ErrTimeout  = errors.New("calendar fetch timed out")                           // ics_fetch_timeout / timeout
	ErrTooLarge = errors.New("calendar is larger than 2 MiB")                      // ics_too_large / too_large
	ErrBusy     = errors.New("too many calendar fetches at once")                  // ics_busy
)

const (
	// MaxBody is the most we read, after transparent gzip decoding.
	MaxBody      = 2 << 20
	maxURL       = 2048
	maxRedirects = 3
	concurrency  = 8
	userAgent    = "DisciplineCalendarImport/1"
)

// Resolver is net.Resolver's lookup (injected in tests).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Fetcher fetches calendars. One per process: its semaphore bounds concurrent outbound fetches.
type Fetcher struct {
	resolver      Resolver
	dial          func(ctx context.Context, network, addr string) (net.Conn, error)
	sem           chan struct{}
	RootCAs       *x509.CertPool // nil = the system roots
	Timeout       time.Duration  // the whole fetch
	HeaderTimeout time.Duration
	Log           *slog.Logger
}

// New returns a Fetcher; a nil resolver uses the system's.
func New(r Resolver) *Fetcher {
	if r == nil {
		r = net.DefaultResolver
	}
	d := &net.Dialer{Timeout: 3 * time.Second, Control: controlRemote}
	return &Fetcher{resolver: r, dial: d.DialContext, sem: make(chan struct{}, concurrency),
		Timeout: 10 * time.Second, HeaderTimeout: 5 * time.Second, Log: slog.Default()}
}

var blockedSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa"}

// Normalize checks a user's calendar URL (scheme, userinfo, port, length, host name) and returns it
// as https with a lower-case IDNA host and no port.
func Normalize(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURL {
		return nil, ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalid
	}
	switch strings.ToLower(u.Scheme) {
	case "webcal", "https":
		u.Scheme = "https"
	default:
		return nil, ErrInvalid
	}
	if u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Opaque != "" {
		return nil, ErrInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return nil, ErrInvalid
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, ErrBlocked // IP literals, v4 and v6
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || len(ascii) > 253 {
		return nil, ErrInvalid
	}
	// A real name ends in a label with a letter; decimal/octal/hex IPv4 forms (2130706433, 0x7f.1) do not.
	labels := strings.Split(ascii, ".")
	if !strings.ContainsAny(labels[len(labels)-1], "abcdefghijklmnopqrstuvwxyz") {
		return nil, ErrBlocked
	}
	if ascii == "localhost" {
		return nil, ErrBlocked
	}
	for _, s := range blockedSuffixes {
		if strings.HasSuffix(ascii, s) {
			return nil, ErrBlocked
		}
	}
	u.Host = ascii
	return u, nil
}

var specialPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "fc00::/7", "64:ff9b::/96", "2002::/16", "2001:db8::/32"} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// Allowed reports whether a resolved address may be connected to.
func Allowed(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range specialPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// controlRemote re-checks the socket's actual remote address just before connecting.
func controlRemote(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !Allowed(ap.Addr()) {
		return ErrBlocked
	}
	return nil
}

// dialContext resolves the name itself; every answer must be allowed (no "try the next one"),
// then it connects to that checked address, so DNS rebinding can't swap it.
func (f *Fetcher) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrInvalid
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port != 443 {
		return nil, ErrBlocked
	}
	ips, err := f.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, ErrFailed
	}
	for _, ip := range ips {
		if !Allowed(ip) {
			return nil, ErrBlocked
		}
	}
	return f.dial(ctx, network, netip.AddrPortFrom(ips[0].Unmap(), uint16(port)).String())
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrBlocked), errors.Is(err, ErrInvalid):
		return ErrBlocked // a redirect to http or to a blocked host
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	return ErrFailed
}

var errTooManyRedirects = errors.New("too many redirects")

func outcomeOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrBlocked):
		return "blocked"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	case errors.Is(err, ErrBusy):
		return "busy"
	}
	return "failed"
}

// Fetch returns the calendar body (at most 2 MiB) and the host. Errors are the Err* values above.
func (f *Fetcher) Fetch(ctx context.Context, raw string) (body []byte, host string, err error) {
	start := time.Now()
	defer func() {
		// Only the host, the outcome, the duration and the size: never the path, query, an IP or body bytes.
		f.Log.Info("calendar fetch", "host", host, "outcome", outcomeOf(err), "ms", time.Since(start).Milliseconds(), "bytes", len(body))
	}()
	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	default:
		return nil, "", ErrBusy
	}
	u, err := Normalize(raw)
	if err != nil {
		return nil, "", err
	}
	host = u.Hostname()
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	transport := &http.Transport{
		Proxy:                  nil, // never HTTP_PROXY
		DialContext:            f.dialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: f.RootCAs},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  f.HeaderTimeout,
		DisableKeepAlives:      true, // a redirect dials (and is checked) again
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errTooManyRedirects
		}
		_, err := Normalize(req.URL.String())
		return err
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, host, ErrInvalid
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/calendar")
	resp, err := client.Do(req)
	if err != nil {
		return nil, host, classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, host, ErrFailed
	}
	if resp.ContentLength > MaxBody {
		return nil, host, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, host, classify(err)
	}
	if len(data) > MaxBody {
		return nil, host, ErrTooLarge
	}
	return data, host, nil
}
