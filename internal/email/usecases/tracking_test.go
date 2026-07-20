package usecases

import (
	"encoding/base64"
	"net/url"
	"testing"
)

func TestSendEmailUsecase_InjectTracking(t *testing.T) {
	u := &sendEmailUsecase{
		baseURL: "http://localhost",
	}

	tenantID := "t1"
	messageID := "m1"
	recipient := "user@example.com"
	recipientEncoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))

	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "Simple link",
			html:     `<html><body><a href="https://example.com">Click</a></body></html>`,
			expected: `<html><body><a href="http://localhost/track/click/t1/m1/` + recipientEncoded + `?url=https%3A%2F%2Fexample.com">Click</a><img src="http://localhost/track/open/t1/m1/` + recipientEncoded + `" width="1" height="1" style="display:none"></body></html>`,
		},
		{
			name:     "Link with query params",
			html:     `<html><body><a href="https://example.com?a=1&b=2">Click</a></body></html>`,
			expected: `<html><body><a href="http://localhost/track/click/t1/m1/` + recipientEncoded + `?url=https%3A%2F%2Fexample.com%3Fa%3D1%26b%3D2">Click</a><img src="http://localhost/track/open/t1/m1/` + recipientEncoded + `" width="1" height="1" style="display:none"></body></html>`,
		},
		{
			name:     "Link with HTML entities",
			html:     `<html><body><a href="https://example.com?a=1&amp;b=2">Click</a></body></html>`,
			expected: `<html><body><a href="http://localhost/track/click/t1/m1/` + recipientEncoded + `?url=https%3A%2F%2Fexample.com%3Fa%3D1%26b%3D2">Click</a><img src="http://localhost/track/open/t1/m1/` + recipientEncoded + `" width="1" height="1" style="display:none"></body></html>`,
		},
		{
			name:     "Skip javascript and tel",
			html:     `<html><body><a href="javascript:alert(1)">JS</a><a href="tel:123">Tel</a></body></html>`,
			expected: `<html><body><a href="javascript:alert(1)">JS</a><a href="tel:123">Tel</a><img src="http://localhost/track/open/t1/m1/` + recipientEncoded + `" width="1" height="1" style="display:none"></body></html>`,
		},
		{
			name:     "Single quotes and spaces",
			html:     `<html><body><a  href = 'https://example.com' >Click</a></body></html>`,
			expected: `<html><body><a  href="http://localhost/track/click/t1/m1/` + recipientEncoded + `?url=https%3A%2F%2Fexample.com" >Click</a><img src="http://localhost/track/open/t1/m1/` + recipientEncoded + `" width="1" height="1" style="display:none"></body></html>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := u.injectTracking(tenantID, messageID, recipient, tc.html)
			if got != tc.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", tc.expected, got)
			}
		})
	}
}

func TestTrackingHandler_HandleClick(t *testing.T) {
	// This is a bit more involved because it needs a mock usecase
	// But we can check the URL decoding logic directly

	rawURL := "https://example.com?a=1&amp;b=2"
	encodedURL := url.QueryEscape(rawURL)

	u, err := url.Parse("http://localhost/track/click/t1/m1/recip?url=" + encodedURL)
	if err != nil {
		t.Fatal(err)
	}

	targetURL := u.Query().Get("url")
	if targetURL != rawURL {
		t.Errorf("expected %s, got %s", rawURL, targetURL)
	}
}
