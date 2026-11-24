package calendarfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDNS resolves names from a table; calls counts lookups per name (for DNS rebinding).
type fakeDNS struct {
	table map[string][]string
	calls map[string]int
	next  map[string][]string // answer from the second lookup on (rebinding)
}

func (f *fakeDNS) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	answers := f.table[host]
	if f.calls[host] > 1 && f.next[host] != nil {
		answers = f.next[host]
	}
	if answers == nil {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, a := range answers {
		out = append(out, netip.MustParseAddr(a))
	}
	return out, nil
}

// testFetcher dials every allowed public address to the TLS test server; a blocked address must
// never reach the dialer (the private server below counts any request it gets).
func testFetcher(t *testing.T, srv *httptest.Server, dns *fakeDNS) (*Fetcher, *atomic.Int32) {
	t.Helper()
	var dials atomic.Int32
	f := New(dns)
	f.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	f.Timeout, f.HeaderTimeout = 2*time.Second, 500*time.Millisecond
	return f, &dials
}

func publicDNS() *fakeDNS {
	return &fakeDNS{table: map[string][]string{"example.com": {"93.184.216.34"}}}
}

func TestFetch_ReadsACalendar(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "DisciplineCalendarImport/1" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected headers %v", r.Header)
		}
		fmt.Fprint(w, "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	}))
	defer srv.Close()
	f, _ := testFetcher(t, srv, publicDNS())
	body, host, err := f.Fetch(context.Background(), "webcal://Example.com/cal.ics")
	if err != nil || host != "example.com" || !strings.HasPrefix(string(body), "BEGIN:VCALENDAR") {
		t.Fatalf("%q %q %v", body, host, err)
	}
}

func TestNormalize_RejectsWhatIsNotASecretCalendarAddress(t *testing.T) {
	invalid := []string{"http://example.com/c.ics", "ftp://example.com/c", "https://user:pw@example.com/c", "https://example.com:8443/c",
		"https://" + strings.Repeat("a", 254) + ".com/", "https://example.com/" + strings.Repeat("x", 2048), "not a url", "https:///nohost"}
	for _, raw := range invalid {
		if _, err := Normalize(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", raw, err)
		}
	}
	blocked := []string{"https://127.0.0.1/c", "https://[::1]/c", "https://2130706433/c", "https://0x7f.1/c", "https://localhost/c",
		"https://a.localhost/c", "https://printer.local/c", "https://metadata.railway.internal/c", "https://foo.internal/c",
		"https://router.home.arpa/c", "https://017700000001/c"}
	for _, raw := range blocked {
		if _, err := Normalize(raw); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: want ErrBlocked, got %v", raw, err)
		}
	}
	if u, err := Normalize("https://Example.COM.:443/c.ics"); err != nil || u.Host != "example.com" {
		t.Fatalf("lower-cased, trailing dot and :443 removed: %v %v", u, err)
	}
}

func TestFetch_BlocksPrivateAndSpecialAddresses(t *testing.T) {
	var hits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer private.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for _, ip := range []string{"10.0.0.5", "169.254.169.254", "fd12::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "100.64.0.1",
		"192.0.2.1", "198.18.0.1", "0.0.0.1", "240.0.0.1", "2002:a00:1::", "2001:db8::1", "127.0.0.1", "::1", "224.0.0.1"} {
		f, dials := testFetcher(t, srv, &fakeDNS{table: map[string][]string{"example.com": {ip}}})
		if _, _, err := f.Fetch(context.Background(), "https://example.com/c.ics"); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: want ErrBlocked, got %v", ip, err)
		}
		if dials.Load() != 0 {
			t.Errorf("%s: a blocked address must never be dialled", ip)
		}
	}
	// One public and one private address: the fetch fails, no "try the next one".
	f, _ := testFetcher(t, srv, &fakeDNS{table: map[string][]string{"example.com": {"93.184.216.34", "10.0.0.5"}}})
	if _, _, err := f.Fetch(context.Background(), "https://example.com/c.ics"); !errors.Is(err, ErrBlocked) {
		t.Errorf("mixed answers: want ErrBlocked, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("no server got a request, got %d", hits.Load())
	}
}

func TestFetch_Redirects(t *testing.T) {
	var target string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, "http://example.com/c.ics", http.StatusFound)
		case "/to-private":
			http.Redirect(w, r, "https://intranet.example.org/c.ics", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "https://example.com/loop", http.StatusFound)
		case "/once":
			http.Redirect(w, r, "https://example.com"+target, http.StatusFound)
		default:
			fmt.Fprint(w, "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
		}
	}))
	defer srv.Close()
	dns := &fakeDNS{table: map[string][]string{"example.com": {"93.184.216.34"}, "intranet.example.org": {"10.1.2.3"}}}
	f, _ := testFetcher(t, srv, dns)
	for path, want := range map[string]error{"/to-http": ErrBlocked, "/to-private": ErrBlocked, "/loop": ErrFailed} {
		if _, _, err := f.Fetch(context.Background(), "https://example.com"+path); !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", path, want, err)
		}
	}
	target = "/ok"
	if _, _, err := f.Fetch(context.Background(), "https://example.com/once"); err != nil {
		t.Errorf("one redirect to an allowed host is fine: %v", err)
	}
	// DNS rebinding: the first lookup is public, the next (the redirect's new connection) private.
	rebind := &fakeDNS{table: map[string][]string{"example.com": {"93.184.216.34"}}, next: map[string][]string{"example.com": {"10.0.0.9"}}}
	f2, _ := testFetcher(t, srv, rebind)
	if _, _, err := f2.Fetch(context.Background(), "https://example.com/once"); !errors.Is(err, ErrBlocked) {
		t.Errorf("rebinding: want ErrBlocked, got %v", err)
	}
}

func TestFetch_LimitsAndStatus(t *testing.T) {
	big := bytes.Repeat([]byte("X"), 3<<20)
	var bomb bytes.Buffer
	gz := gzip.NewWriter(&bomb)
	_, _ = gz.Write(big)
	_ = gz.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			_, _ = w.Write(big)
		case "/length":
			w.Header().Set("Content-Length", fmt.Sprint(3<<20))
			_, _ = w.Write([]byte("x"))
		case "/bomb":
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(bomb.Bytes())
		case "/slow-headers":
			time.Sleep(time.Second)
		case "/slow-body":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			time.Sleep(3 * time.Second)
		case "/404":
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f, _ := testFetcher(t, srv, publicDNS())
	for path, want := range map[string]error{"/big": ErrTooLarge, "/length": ErrTooLarge, "/bomb": ErrTooLarge,
		"/slow-headers": ErrTimeout, "/slow-body": ErrTimeout, "/404": ErrFailed} {
		if _, _, err := f.Fetch(context.Background(), "https://example.com"+path); !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", path, want, err)
		}
	}
}

func TestFetch_AtMostEightAtOnce(t *testing.T) {
	f := New(publicDNS())
	for i := 0; i < cap(f.sem); i++ {
		f.sem <- struct{}{}
	}
	if _, _, err := f.Fetch(context.Background(), "https://example.com/c.ics"); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	if cap(f.sem) != 8 {
		t.Fatalf("8 concurrent fetches, got %d", cap(f.sem))
	}
}

func TestFetch_LogsOnlyHostOutcomeDurationBytes(t *testing.T) {
	var buf bytes.Buffer
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "BEGIN:VCALENDAR") }))
	defer srv.Close()
	f, _ := testFetcher(t, srv, &fakeDNS{table: map[string][]string{"example.com": {"93.184.216.34"}, "blocked.example.com": {"10.9.9.9"}}})
	f.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	_, _, _ = f.Fetch(context.Background(), "https://example.com/private-secret-path.ics?token=abc")
	_, _, _ = f.Fetch(context.Background(), "https://blocked.example.com/private-secret-path.ics")
	out := buf.String()
	for _, leak := range []string{"private-secret-path", "token", "abc", "10.9.9.9", "BEGIN:VCALENDAR"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"host":"example.com"`) || !strings.Contains(out, `"outcome":"ok"`) || !strings.Contains(out, `"outcome":"blocked"`) {
		t.Fatalf("log should name host and outcome: %s", out)
	}
}
