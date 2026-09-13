package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls the control API with an account session token.
// Configure its fields before use; do not mutate them during concurrent requests.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a control client with a 30-second HTTP timeout.
// An empty token is sufficient for unauthenticated sign-in requests.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Endpoint describes an account-owned connection reported by the control API.
type Endpoint struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	DisplayName string   `json:"display_name"`
	Engine      string   `json:"engine"`
	Region      string   `json:"region"`
	URL         string   `json:"url"`
	Online      bool     `json:"online"`
	Active      bool     `json:"active"`
	CORSOrigins []string `json:"cors_origins"`
	Trial       bool     `json:"trial"`
}

// CreatedEndpoint includes the credentials issued when a connection is created.
// Persist those credentials securely; do not log the response.
type CreatedEndpoint struct {
	Endpoint   Endpoint `json:"endpoint"`
	AgentToken string   `json:"agent_token"`
	APIKey     string   `json:"api_key"`
	RelayURL   string   `json:"relay_url"`
	// Trial-mode creation (account not entitled) adds the allowance state;
	// a paid creation lists the temporary trial endpoints it replaced.
	Trial                    bool       `json:"trial"`
	TrialRemaining           int        `json:"trial_remaining"`
	TrialTransferRemaining   int64      `json:"trial_transfer_remaining"`
	TrialExpiresAt           *time.Time `json:"trial_expires_at,omitempty"`
	RequestedNameIgnored     bool       `json:"requested_name_ignored"`
	ReplacedTrialEndpointIDs []string   `json:"replaced_trial_endpoint_ids,omitempty"`
}

// APIKey contains key metadata, not the reusable secret.
type APIKey struct {
	ID         string     `json:"id"`
	EndpointID string     `json:"endpoint_id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Account is the client-visible account and allowance state.
type Account struct {
	MaxEndpoints           *int       `json:"max_endpoints,omitempty"`
	ID                     string     `json:"id"`
	Email                  string     `json:"email"`
	BillingState           string     `json:"billing_state"`
	BillingGraceUntil      *time.Time `json:"billing_grace_until,omitempty"`
	TrialStatus            string     `json:"trial_status"`
	TrialRemaining         int        `json:"trial_remaining"`
	TrialExpiresAt         *time.Time `json:"trial_expires_at,omitempty"`
	TrialRequestsUsed      int        `json:"trial_requests_used"`
	TrialTransferUsed      int64      `json:"trial_transfer_used"`
	TrialTransferRemaining int64      `json:"trial_transfer_remaining"`
	TrialStartedAt         *time.Time `json:"trial_started_at,omitempty"`
}

// DeviceAuthorization holds the approval codes and polling interval for CLI sign-in.
// ExpiresIn and Interval are expressed in seconds.
type DeviceAuthorization struct {
	DeviceCode string `json:"device_code"`
	// UserCode is shown to the person so they can confirm, on the emailed
	// approval page, that they are approving this terminal and not an attacker's.
	UserCode                   string `json:"user_code"`
	VerificationURI            string `json:"verification_uri"`
	DevelopmentVerificationURL string `json:"development_verification_url"`
	ExpiresIn                  int    `json:"expires_in"`
	Interval                   int    `json:"interval"`
}

// StartDeviceAuthorization requests an email approval link for CLI sign-in.
func (c *Client) StartDeviceAuthorization(ctx context.Context, email string) (DeviceAuthorization, error) {
	var out DeviceAuthorization
	err := c.doUnauthenticated(ctx, http.MethodPost, "/v1/auth/device/start", map[string]string{"email": email}, &out)
	return out, err
}

// SignOut revokes the client's session token server-side. The route is
// idempotent, so an already-revoked or unknown token is not an error.
func (c *Client) SignOut(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/sign-out", nil, nil)
}

// PollDeviceAuthorization returns the account token after approval.
// pending is true while approval is outstanding; callers should respect Interval.
func (c *Client) PollDeviceAuthorization(ctx context.Context, deviceCode string) (token string, pending bool, err error) {
	var out struct {
		AccountToken string `json:"account_token"`
		Status       string `json:"status"`
	}
	path := "/v1/auth/device/token?device_code=" + url.QueryEscape(deviceCode)
	err = c.doUnauthenticated(ctx, http.MethodGet, path, nil, &out)
	if apiErr, ok := err.(*APIError); ok && apiErr.Status == http.StatusAccepted {
		return "", true, nil
	}
	if err == nil && out.Status == "authorization_pending" {
		return "", true, nil
	}
	return out.AccountToken, false, err
}

// APIError preserves the control API status and error code. Message is server-supplied.
type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// CreateEndpoint creates an endpoint; name may be empty, in which case a
// trial account receives an auto-generated temporary address.
func (c *Client) CreateEndpoint(ctx context.Context, name, displayName, engine, region string, corsOrigins []string) (CreatedEndpoint, error) {
	var out CreatedEndpoint
	err := c.do(ctx, http.MethodPost, "/v1/endpoints", map[string]any{"name": name, "display_name": displayName, "engine": engine, "region": region, "cors_origins": corsOrigins}, &out)
	return out, err
}

// MoveTrialEndpoint replaces only the explicitly selected trial connection.
func (c *Client) MoveTrialEndpoint(ctx context.Context, id, displayName, engine, region string, corsOrigins []string) (CreatedEndpoint, error) {
	var out CreatedEndpoint
	err := c.do(ctx, http.MethodPost, "/v1/endpoints/"+url.PathEscape(id)+"/move-trial", map[string]any{"display_name": displayName, "engine": engine, "region": region, "cors_origins": corsOrigins}, &out)
	return out, err
}

// Me returns the current account and its reported service allowances.
func (c *Client) Me(ctx context.Context) (Account, error) {
	var out Account
	err := c.do(ctx, http.MethodGet, "/v1/me", nil, &out)
	return out, err
}

// CreateCheckout returns a hosted payment URL; it does not complete a purchase.
func (c *Client) CreateCheckout(ctx context.Context) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/billing/checkout", struct{}{}, &out)
	return out.URL, err
}

// CreateBillingPortal returns a hosted account billing-management URL.
func (c *Client) CreateBillingPortal(ctx context.Context) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/billing/portal", struct{}{}, &out)
	return out.URL, err
}

// ListEndpoints returns the current account's connections.
func (c *Client) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	var out struct {
		Data []Endpoint `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/endpoints", nil, &out)
	return out.Data, err
}

// DeleteEndpoint removes the selected connection from the hosted service.
func (c *Client) DeleteEndpoint(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/endpoints/"+id, nil, nil)
}

// CreateKey returns metadata and the newly issued plaintext inference key.
// The caller must store or display the secret without logging it.
func (c *Client) CreateKey(ctx context.Context, endpointID, name string) (APIKey, string, error) {
	var out struct {
		Key    APIKey `json:"key"`
		Secret string `json:"secret"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/endpoints/"+endpointID+"/keys", map[string]string{"name": name}, &out)
	return out.Key, out.Secret, err
}

// ListKeys returns key metadata for an endpoint; it does not reveal secrets.
func (c *Client) ListKeys(ctx context.Context, endpointID string) ([]APIKey, error) {
	var out struct {
		Data []APIKey `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/endpoints/"+endpointID+"/keys", nil, &out)
	return out.Data, err
}

// RevokeKey revokes the selected inference key through the control API.
func (c *Client) RevokeKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/keys/"+id, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	return c.request(ctx, method, path, input, output, true)
}
func (c *Client) doUnauthenticated(ctx context.Context, method, path string, input, output any) error {
	return c.request(ctx, method, path, input, output, false)
}
func (c *Client) request(ctx context.Context, method, path string, input, output any, authenticated bool) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
			return &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
		}
		return &APIError{Status: resp.StatusCode, Message: "control API returned " + resp.Status}
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(output); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// StartEmailCode begins the email-code sign-in shared by the website and desktop apps.
func (c *Client) StartEmailCode(ctx context.Context, email string) (string, error) {
	var out struct {
		ChallengeID string `json:"challenge_id"`
	}
	err := c.doUnauthenticated(ctx, http.MethodPost, "/v1/auth/web/code/start", map[string]string{"email": email}, &out)
	return out.ChallengeID, err
}

// VerifyEmailCode exchanges an email challenge and code for a session and account.
func (c *Client) VerifyEmailCode(ctx context.Context, challenge, code string) (string, Account, error) {
	var out struct {
		SessionToken string  `json:"session_token"`
		Account      Account `json:"account"`
	}
	err := c.doUnauthenticated(ctx, http.MethodPost, "/v1/auth/web/code/verify", map[string]string{"challenge_id": challenge, "code": code}, &out)
	return out.SessionToken, out.Account, err
}
