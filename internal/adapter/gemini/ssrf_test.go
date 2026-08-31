package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Inlining makes the gateway issue a GET at an address the client chose. The
// scheme check is not what stops that reaching the machine's own network: a
// loopback, private or link-local address has to be refused outright, and
// 169.254.169.254 is the one that hands out cloud credentials.
func TestPartRefusesAnAddressOffThePublicInternet(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("secret"))
	}))
	defer up.Close()

	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{URL: up.URL + "/a.png"}, "image")
	if got != nil {
		t.Fatalf("part = %v; a loopback address must not be fetched", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Reason, "could not inline") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPublicAddrRejectsEveryReservedRange(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.13.2.9", "0.0.0.0", "::1", "::",
		"10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "fe80::1",
		"fc00::1", "fd12:3456::1",
		"100.64.0.1", "192.0.0.1", "198.18.0.1", "240.0.0.1", "255.255.255.255",
		"224.0.0.1", "ff02::1",
		// An IPv4 address wearing an IPv6 coat is the same address.
		"::ffff:127.0.0.1", "::ffff:10.0.0.1",
		// NAT64 and 6to4 both embed an IPv4 address that a gateway will route.
		"64:ff9b::7f00:1", "2002:7f00:1::1",
	}
	for _, s := range blocked {
		if addrIsPublic(mustAddr(t, s)) {
			t.Errorf("%s is treated as public", s)
		}
	}

	allowed := []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "2606:4700::1111"}
	for _, s := range allowed {
		if !addrIsPublic(mustAddr(t, s)) {
			t.Errorf("%s is treated as private", s)
		}
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return a
}
