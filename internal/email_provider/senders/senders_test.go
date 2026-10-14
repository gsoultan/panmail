package senders

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeCanonicalisesEachForm(t *testing.T) {
	got, err := Normalize([]string{
		" Billing@Acme.com ",
		"acme.org",
		"@acme.net",
		"*@Acme.io",
		"Acme Ops <ops@acme.dev>",
		"",
		"billing@acme.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"billing@acme.com", "*@acme.org", "*@acme.net", "*@acme.io", "ops@acme.dev"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize = %v, want %v", got, want)
	}
}

func TestNormalizeRefusesWhatMatchCouldNotUse(t *testing.T) {
	for _, entry := range []string{
		"*", "*@", "*@*",
		"*.acme.com", "*@*.acme.com", "bill*@acme.com",
		"billing@", "@",
		"billing@acme", "billing@-acme.com", "bill ing@acme.com",
	} {
		_, err := Normalize([]string{"ok@acme.com", entry})
		var invalid *InvalidEntryError
		if !errors.As(err, &invalid) {
			t.Errorf("Normalize(%q) error = %v, want an InvalidEntryError", entry, err)
			continue
		}
		if invalid.Entry != entry {
			t.Errorf("Normalize(%q) named %q", entry, invalid.Entry)
		}
	}
}

func TestMatchAddress(t *testing.T) {
	list := []string{"*@acme.com", "billing@acme-pay.io"}
	tests := []struct {
		entries []string
		address string
		want    Match
	}{
		{nil, "anyone@anywhere.com", Unrestricted},
		{list, "ops@acme.com", ByDomain},
		{list, "OPS@ACME.COM", ByDomain},
		{list, "billing@acme-pay.io", ByAddress},
		{list, "receipts@acme-pay.io", Refused},
		// A domain entry does not reach subdomains.
		{list, "ops@mail.acme.com", Refused},
		// Plus addressing is a different address.
		{list, "billing+eu@acme-pay.io", Refused},
		// Rows stored before addresses existed hold bare domains.
		{[]string{"Acme.com"}, "ops@acme.com", ByDomain},
		// An exact entry wins over a domain entry for routing preference.
		{[]string{"*@acme.com", "ops@acme.com"}, "ops@acme.com", ByAddress},
	}
	for _, tt := range tests {
		if got := MatchAddress(tt.entries, tt.address); got != tt.want {
			t.Errorf("MatchAddress(%v, %q) = %v, want %v", tt.entries, tt.address, got, tt.want)
		}
	}
}

func TestAddressReadsTheDisplayNameForm(t *testing.T) {
	tests := []struct {
		from    string
		want    string
		wantErr bool
	}{
		{"alice@Example.com", "alice@example.com", false},
		{"Alice <alice@example.com>", "alice@example.com", false},
		{"no-at-sign", "", true},
		{"trailing@", "", true},
	}
	for _, tt := range tests {
		got, err := Address(tt.from)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Address(%q) = %q, %v; want %q, err %v", tt.from, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestCanonicalKeepsWhatItCannotParse(t *testing.T) {
	// Dropping an unreadable stored entry would widen the list, and dropping
	// the last one would lift the restriction.
	got := Canonical([]string{"example.com", " weird entry@x ", "example.com"})
	want := []string{"*@example.com", "weird entry@x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Canonical = %v, want %v", got, want)
	}
}

func TestDomains(t *testing.T) {
	list := []string{"*@acme.com", "billing@acme-pay.io", "ops@acme.com", "receipts@acme-pay.io"}
	if got, want := Domains(list), []string{"acme.com", "acme-pay.io"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Domains = %v, want %v", got, want)
	}
}
