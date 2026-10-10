package entities

import "testing"

func TestSplitSendGridWebhookSecret(t *testing.T) {
	const key = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE+ab/cd=="
	tests := []struct {
		name, secret, wantKey, wantInbound string
	}{
		{"key only, as every existing provider stores it", key, key, ""},
		{"credential only", "inbound=parse:s3cret", "", "parse:s3cret"},
		{"both", key + " inbound=parse:s3cret", key, "parse:s3cret"},
		{"credential first", "inbound=parse:s3cret\n" + key, key, "parse:s3cret"},
		{"password alone", "inbound=s3cret", "", "s3cret"},
		// "inbound=" inside a value is not the token: it has to begin one.
		{"not at a token boundary", "xinbound=parse:s3cret", "xinbound=parse:s3cret", ""},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotInbound := SplitSendGridWebhookSecret(tt.secret)
			if gotKey != tt.wantKey || gotInbound != tt.wantInbound {
				t.Errorf("Split(%q) = %q, %q; want %q, %q",
					tt.secret, gotKey, gotInbound, tt.wantKey, tt.wantInbound)
			}
		})
	}
}
