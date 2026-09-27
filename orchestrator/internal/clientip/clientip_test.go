package clientip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func request(remote, forwarded string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remote
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	return req
}

func TestClientIP(t *testing.T) {
	proxyIP := netip.MustParseAddr("172.20.0.5")
	r := &Resolver{entries: []string{"proxy", "10.0.0.0/8"}, LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "proxy" {
			return []netip.Addr{proxyIP}, nil
		}
		return nil, errors.New("unknown host")
	}}
	r.Refresh(context.Background())

	cases := []struct{ name, remote, forwarded, want string }{
		{"direct client ignores the header", "198.51.100.7:1000", "203.0.113.9", "198.51.100.7"},
		{"trusted proxy passes the client", "172.20.0.5:1000", "203.0.113.9", "203.0.113.9"},
		{"spoofed hops before the real client are ignored", "172.20.0.5:1000", "1.2.3.4, 203.0.113.9", "203.0.113.9"},
		{"chained trusted proxies are skipped", "172.20.0.5:1000", "203.0.113.9, 10.1.2.3", "203.0.113.9"},
		{"trusted proxy without header", "172.20.0.5:1000", "", "172.20.0.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.ClientIP(request(c.remote, c.forwarded)); got != c.want {
				t.Fatalf("ClientIP = %s, want %s", got, c.want)
			}
		})
	}

	// When the proxy moves, the refresh follows it.
	proxyIP = netip.MustParseAddr("172.20.0.9")
	r.Refresh(context.Background())
	if got := r.ClientIP(request("172.20.0.5:1000", "203.0.113.9")); got != "172.20.0.5" {
		t.Fatalf("the old proxy address must no longer be trusted, got %s", got)
	}
}
