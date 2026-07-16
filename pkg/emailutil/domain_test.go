package emailutil

import "testing"

func TestGetRootDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"mail.example.com", "example.com"},
		{"sub.mail.example.com", "example.com"},
		{"user@example.com", "example.com"},
		{"user@mail.example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"localhost", "localhost"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := GetRootDomain(tc.input)
			if got != tc.expected {
				t.Errorf("GetRootDomain(%s) = %s; want %s", tc.input, got, tc.expected)
			}
		})
	}
}
