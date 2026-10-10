package egress

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestDisallowedRanges(t *testing.T) {
	cases := map[string]bool{
		// Refused.
		"10.1.2.3":        true,
		"172.16.0.1":      true,
		"172.31.255.254":  true,
		"192.168.1.1":     true,
		"127.0.0.1":       true,
		"127.8.9.10":      true,
		"169.254.169.254": true, // cloud metadata
		"100.64.0.1":      true, // CGNAT
		"100.127.255.254": true,
		"224.0.0.1":       true,
		"239.255.255.250": true,
		"0.0.0.0":         true,
		"0.1.2.3":         true,
		"::":              true,
		"::1":             true,
		"fe80::1":         true,
		"fe80::1%eth0":    true,
		"fc00::1":         true,
		"fd12:3456::1":    true,
		"ff02::1":         true,
		"ff05::2":         true,
		// IPv4-mapped and NAT64 forms of private addresses are the same
		// address on the wire.
		"::ffff:127.0.0.1":   true,
		"::ffff:10.0.0.1":    true,
		"::ffff:169.254.0.1": true,
		"64:ff9b::7f00:1":    true,
		"64:ff9b::a9fe:a9fe": true,
		"::ffff:100.64.0.1":  true,
		// Allowed.
		"8.8.8.8":              false,
		"93.184.216.34":        false,
		"100.63.255.255":       false, // just below CGNAT
		"100.128.0.0":          false, // just above
		"172.32.0.1":           false,
		"2606:4700:4700::1111": false,
		"::ffff:8.8.8.8":       false,
		"64:ff9b::808:808":     false,
	}
	for addr, want := range cases {
		ip := netip.MustParseAddr(addr)
		if got := Disallowed(ip); got != want {
			t.Errorf("Disallowed(%s) = %v, want %v", addr, got, want)
		}
	}
	if !Disallowed(netip.Addr{}) {
		t.Error("the zero address was allowed")
	}
}

func TestControlRefusesADisallowedDialAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:80", "[::1]:443", "[::ffff:10.0.0.1]:80", "[fe80::1%eth0]:80", "nonsense"} {
		if err := Control("tcp", address, nil); !errors.Is(err, ErrDisallowed) {
			t.Errorf("Control(%q) = %v, want ErrDisallowed", address, err)
		}
	}
	for _, address := range []string{"93.184.216.34:443", "[2606:4700:4700::1111]:443"} {
		if err := Control("tcp", address, nil); err != nil {
			t.Errorf("Control(%q) = %v, want allowed", address, err)
		}
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addrs, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

func TestCheckURL(t *testing.T) {
	resolver := fakeResolver{
		"hooks.example.com": {netip.MustParseAddr("93.184.216.34")},
		"internal.example":  {netip.MustParseAddr("10.0.0.5")},
		// Any one private answer is enough to refuse: which one the dialer
		// picks is not ours to predict.
		"mixed.example": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("::1")},
	}
	refused := []string{
		"http://127.0.0.1:8080/hook",
		"http://[::1]/hook",
		"http://[::ffff:192.168.0.1]/hook",
		"http://169.254.169.254/latest/meta-data",
		"https://internal.example/hook",
		"https://mixed.example/hook",
	}
	for _, raw := range refused {
		if err := CheckURL(context.Background(), resolver, raw); !errors.Is(err, ErrDisallowed) {
			t.Errorf("CheckURL(%q) = %v, want ErrDisallowed", raw, err)
		}
	}
	allowed := []string{
		"https://hooks.example.com/panmail",
		"https://93.184.216.34/hook",
		// Unresolvable now; the dial-time check decides later.
		"https://not-yet.example/hook",
	}
	for _, raw := range allowed {
		if err := CheckURL(context.Background(), resolver, raw); err != nil {
			t.Errorf("CheckURL(%q) = %v, want allowed", raw, err)
		}
	}
	if err := CheckURL(context.Background(), resolver, "not a url at all://"); err == nil {
		t.Error("a malformed URL was accepted")
	}
}
