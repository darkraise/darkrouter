package gemini

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
)

var errBlockedAddress = errors.New("gemini: the URL resolves to an address off the public internet")

// nonPublic are the ranges no client-supplied URL may reach. The ones the
// netip predicates already cover are not repeated here.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and the broadcast address
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("2002::/16"),     // 6to4
}

// addrIsPublic reports whether an address is one the gateway may be pointed at
// on a client's behalf.
//
// The two embedded-IPv4 prefixes are here because a NAT64 or 6to4 gateway
// routes them to the IPv4 address they carry, which puts 127.0.0.1 back within
// reach of an address that passes every IPv6 predicate.
func addrIsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// guardConn is a net.Dialer Control function, which the runtime calls with the
// resolved address immediately before connecting.
//
// Checking here rather than on the URL's hostname is what closes DNS
// rebinding: a name that answered with a public address when it was validated
// and a private one when it was dialled is refused at the dial, because this
// sees the address the connection will actually use.
func guardConn(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddress
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !addrIsPublic(ip) {
		return errBlockedAddress
	}
	return nil
}
