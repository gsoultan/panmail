// Package oauth2 exchanges a stored refresh token for an access token.
//
// Gmail and Office 365 are retiring password authentication for SMTP, so a
// gateway that can only send a password will eventually stop delivering through
// either. Both accept OAuth2 over SMTP via the XOAUTH2 mechanism, which needs a
// short-lived access token minted from a long-lived refresh token.
//
// gsmail.CachingTokenSource handles caching, renewal timing and single-flighting
// concurrent refreshes; this package supplies only the grant itself.
package oauth2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gsoultan/gsmail"
)

// maxResponseBytes caps what is read from the identity provider. A token
// response is a few hundred bytes; anything approaching this is a
// misconfigured endpoint or a hostile one, and reading it all would be the
// bug rather than the protection.
const maxResponseBytes = 1 << 20

// defaultExpiry is used when a provider omits expires_in. Short on purpose: a
// token assumed to last longer than it does fails the send it was fetched for,
// while renewing too eagerly only costs a request.
const defaultExpiry = 30 * time.Minute

// Config is what a refresh needs. It is deliberately not the proto type, so
// this package stays independent of the API surface.
type Config struct {
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
	RefreshToken  string
	// Scope is optional. Google ignores it on a refresh; some providers
	// require it to narrow the resulting token.
	Scope string

	// HTTPClient is injectable for tests. Nil uses a client with a timeout,
	// because the default http.Client has none and a hung identity provider
	// would otherwise block a send indefinitely.
	HTTPClient *http.Client
}

func (c Config) validate() error {
	var missing []string
	if c.TokenEndpoint == "" {
		missing = append(missing, "token endpoint")
	}
	if c.ClientID == "" {
		missing = append(missing, "client ID")
	}
	if c.RefreshToken == "" {
		missing = append(missing, "refresh token")
	}
	if len(missing) > 0 {
		return fmt.Errorf("oauth2 configuration is incomplete: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// RefreshFunc returns a function that performs the refresh_token grant.
//
// The returned function is what gsmail.CachingTokenSource wraps, so it is called
// only when the cached token is close to expiry rather than on every send.
func RefreshFunc(cfg Config) gsmail.RefreshFunc {
	return func(ctx context.Context) (string, time.Time, error) {
		if err := cfg.validate(); err != nil {
			// Configuration will not fix itself by trying again.
			return "", time.Time{}, gsmail.NonRetryable(err)
		}

		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {cfg.RefreshToken},
			"client_id":     {cfg.ClientID},
		}
		// Public clients have no secret; sending an empty one makes some
		// providers reject the request outright.
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
		if cfg.Scope != "" {
			form.Set("scope", cfg.Scope)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint,
			strings.NewReader(form.Encode()))
		if err != nil {
			return "", time.Time{}, gsmail.NonRetryable(fmt.Errorf("build token request: %w", err))
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		client := cfg.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Second}
		}

		resp, err := client.Do(req)
		if err != nil {
			// Network trouble is worth retrying; the outbox will try again.
			return "", time.Time{}, fmt.Errorf("token endpoint unreachable: %w", err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
			_ = resp.Body.Close()
		}()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("read token response: %w", err)
		}

		var tr tokenResponse
		// A provider may answer with a non-JSON error page; the status code
		// below still classifies it correctly.
		_ = json.Unmarshal(body, &tr)

		if resp.StatusCode != http.StatusOK {
			err := describeFailure(resp.StatusCode, tr, body)
			// 4xx means the grant itself is wrong — a revoked refresh token, a
			// deleted client, a changed secret. Retrying re-sends the same
			// rejected credential every time the outbox wakes, so it is marked
			// permanent and the operator is told once.
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return "", time.Time{}, gsmail.NonRetryable(err)
			}
			return "", time.Time{}, err
		}

		if tr.AccessToken == "" {
			return "", time.Time{}, gsmail.NonRetryable(
				errors.New("token endpoint returned 200 with no access token"))
		}

		lifetime := time.Duration(tr.ExpiresIn) * time.Second
		if tr.ExpiresIn <= 0 {
			lifetime = defaultExpiry
		}
		return tr.AccessToken, time.Now().Add(lifetime), nil
	}
}

// describeFailure turns a rejected grant into something an operator can act on.
// OAuth2 error codes are terse and the description usually names the actual
// problem, so both are surfaced.
func describeFailure(status int, tr tokenResponse, body []byte) error {
	switch {
	case tr.Error == "invalid_grant":
		return fmt.Errorf("the refresh token was rejected (invalid_grant): it has been revoked, expired, "+
			"or belongs to a different client — reauthorise this provider. %s", tr.ErrorDesc)
	case tr.Error != "":
		return fmt.Errorf("token endpoint returned %s: %s", tr.Error, tr.ErrorDesc)
	default:
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return fmt.Errorf("token endpoint returned HTTP %d: %s", status, snippet)
	}
}
