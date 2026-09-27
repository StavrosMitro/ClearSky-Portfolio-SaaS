// Package clientip identifies the real client behind trusted reverse
// proxies. Entries may be IPs, CIDRs or hostnames (e.g. the Compose service
// "proxy"); hostnames are re-resolved periodically because a container's
// address changes when it is recreated.
package clientip

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Resolver struct {
	mu       sync.RWMutex
	entries  []string
	trusted  []netip.Prefix
	LookupIP func(ctx context.Context, host string) ([]netip.Addr, error)
}

func New(entries []string) *Resolver {
	r := &Resolver{entries: entries, LookupIP: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}}
	r.Refresh(context.Background())
	return r
}

// Refresh resolves every entry; unresolvable hostnames are skipped (and
// logged) so a missing proxy never makes spoofed headers trusted.
func (r *Resolver) Refresh(ctx context.Context) {
	var prefixes []netip.Prefix
	for _, entry := range r.entries {
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		addrs, err := r.LookupIP(lookupCtx, entry)
		cancel()
		if err != nil {
			slog.Warn("trusted proxy not resolvable", "host", entry, "error", err)
			continue
		}
		for _, addr := range addrs {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	r.mu.Lock()
	r.trusted = prefixes
	r.mu.Unlock()
}

// Run refreshes hostnames every interval until ctx ends.
func (r *Resolver) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Refresh(ctx)
		}
	}
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the TCP peer, or, when the peer is a trusted proxy, the
// right-most X-Forwarded-For address that is not itself a trusted proxy.
func (r *Resolver) ClientIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !r.isTrusted(peer.Unmap()) {
		return host
	}
	hops := strings.Split(req.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !r.isTrusted(addr.Unmap()) {
			return addr.String()
		}
	}
	return host
}
