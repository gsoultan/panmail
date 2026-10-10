// Package egress decides which addresses an outbound webhook may reach when
// the operator has asked for webhooks to stay on the public internet.
//
// Webhook URLs are written by tenants. On a single-tenant, self-hosted gateway
// a URL pointing at a service on the same network is the ordinary case, so
// nothing here is on by default. On a shared gateway the same URL is a way for
// one tenant to make the gateway POST into the operator's private network --
// a metadata service, an admin port bound to loopback, a database's HTTP
// interface -- and read back at least whether it answered.
//
// The check that counts is made at dial time, on the address actually being
// connected to (Control). A check made only on the URL when it is saved is
// beaten by a name that resolves to a public address then and a private one
// later, and by a redirect; checking the connection itself is beaten by
// neither. The save-time check (CheckURL) exists to give a tenant a clear
// refusal up front rather than a stream of failed deliveries.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"syscall"
)

// ErrDisallowed is wrapped by every refusal, so a caller can tell a target the
// policy refuses from one that is merely down.
var ErrDisallowed = errors.New("webhook target is a private or reserved address")

var (
	// RFC 6598 shared address space: carrier-grade NAT, and in practice the
	// internal ranges of several cloud providers. netip has no predicate for
	// it.
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	// "This network". Only 0.0.0.0 itself is IsUnspecified, but on Linux any
	// of these reaches the local host.
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	// RFC 6052 NAT64. An address here is an IPv4 address in disguise, and is
	// judged as the one it embeds.
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

// Disallowed reports whether ip is in a range a webhook must not reach:
// private, loopback, link-local, CGNAT, multicast or unspecified, for IPv4
// and IPv6 alike.
func Disallowed(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	// ::ffff:10.0.0.1 is 10.0.0.1 on the wire; judged as IPv6 it would pass
	// every IPv4 check below.
	ip = ip.Unmap().WithZone("")
	if ip.Is6() && nat64.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return ip.IsPrivate() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		cgnat.Contains(ip) ||
		thisNetwork.Contains(ip)
}

// Control is a net.Dialer Control function that refuses a connection to a
// disallowed address. It runs after name resolution, on the address the
// socket is about to connect to, which is what makes it immune to rebinding
// and redirects.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		// Fail closed: an address that cannot be read cannot be shown to be
		// public.
		return fmt.Errorf("%w: unrecognised address %q", ErrDisallowed, address)
	}
	if Disallowed(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrDisallowed, ap.Addr())
	}
	return nil
}

// Resolver is the part of net.Resolver CheckURL uses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// CheckURL refuses a webhook URL whose host is, or currently resolves to, a
// disallowed address. It is the friendly check, for saving a subscription;
// Control is the one that holds.
//
// A name that does not resolve right now is let through: it may be a record
// still being created, and the dial-time check refuses it later if it turns
// out to be private.
func CheckURL(ctx context.Context, resolver Resolver, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("invalid webhook URL: no host")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if Disallowed(ip) {
			return fmt.Errorf("%w: %s", ErrDisallowed, ip)
		}
		return nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	for _, ip := range addrs {
		if Disallowed(ip) {
			return fmt.Errorf("%w: %s resolves to %s", ErrDisallowed, host, ip)
		}
	}
	return nil
}
