package usecases

import (
	"errors"
	"testing"
)

// resolveRecipients replaced a validation loop and a separate de-duplication
// and normalisation pass, so it has to answer exactly what those three did.
// Parsing once instead of twice is the only thing that changed.
func TestResolveRecipientsDerivesBothFormsInOnePass(t *testing.T) {
	testCases := []struct {
		name           string
		to             []string
		cc             []string
		bcc            []string
		wantAddresses  []string
		wantNormalised []string
	}{
		{
			name:           "a single plain address",
			to:             []string{"user@example.net"},
			wantAddresses:  []string{"user@example.net"},
			wantNormalised: []string{"user@example.net"},
		},
		{
			name:           "case and surrounding space are folded away",
			to:             []string{"  User@Example.NET  "},
			wantAddresses:  []string{"user@example.net"},
			wantNormalised: []string{"user@example.net"},
		},
		{
			name:           "a display name is kept for the audience and stripped for the lookup",
			to:             []string{"Alice Smith <alice@example.net>"},
			wantAddresses:  []string{"alice smith <alice@example.net>"},
			wantNormalised: []string{"alice@example.net"},
		},
		{
			name:           "the lists are joined in order",
			to:             []string{"a@example.net"},
			cc:             []string{"b@example.net"},
			bcc:            []string{"c@example.net"},
			wantAddresses:  []string{"a@example.net", "b@example.net", "c@example.net"},
			wantNormalised: []string{"a@example.net", "b@example.net", "c@example.net"},
		},
		{
			name:           "the same person across two lists gets one copy",
			to:             []string{"dup@example.net"},
			cc:             []string{"DUP@example.net"},
			wantAddresses:  []string{"dup@example.net"},
			wantNormalised: []string{"dup@example.net"},
		},
		{
			name:           "a repeat inside one list collapses too",
			to:             []string{"dup@example.net", "dup@example.net"},
			wantAddresses:  []string{"dup@example.net"},
			wantNormalised: []string{"dup@example.net"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRecipients(tc.to, tc.cc, tc.bcc)
			if err != nil {
				t.Fatalf("resolveRecipients() error = %v", err)
			}

			if !equalStringSlices(got.addresses, tc.wantAddresses) {
				t.Errorf("addresses = %v, want %v", got.addresses, tc.wantAddresses)
			}
			if !equalStringSlices(got.normalised, tc.wantNormalised) {
				t.Errorf("normalised = %v, want %v", got.normalised, tc.wantNormalised)
			}
			// The two are read by index against each other, so a length
			// mismatch would send one recipient another's suppression answer.
			if len(got.addresses) != len(got.normalised) {
				t.Errorf("addresses and normalised are different lengths: %d vs %d",
					len(got.addresses), len(got.normalised))
			}
		})
	}
}

func TestResolveRecipientsRejectsWhatCannotBeDelivered(t *testing.T) {
	testCases := []struct {
		name    string
		to      []string
		wantErr error
	}{
		{
			name:    "an empty entry is a caller mistake, not a bad address",
			to:      []string{"ok@example.net", ""},
			wantErr: errEmptyRecipient,
		},
		{
			name: "whitespace only counts as empty",
			to:   []string{"   "},
			// A rejected RCPT TO counts against sending reputation, so an
			// address that cannot possibly be delivered never reaches a
			// provider.
			wantErr: errEmptyRecipient,
		},
		{
			name: "a malformed address is refused",
			to:   []string{"not an address"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveRecipients(tc.to, nil, nil)
			if err == nil {
				t.Fatal("resolveRecipients() accepted an undeliverable list")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveRecipientsWithNoAddresses(t *testing.T) {
	got, err := resolveRecipients(nil, nil, nil)
	if err != nil {
		t.Fatalf("resolveRecipients() error = %v", err)
	}
	if len(got.addresses) != 0 || len(got.normalised) != 0 {
		t.Errorf("got %d addresses for empty lists", len(got.addresses))
	}
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
