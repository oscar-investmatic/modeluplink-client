// Package upstream defines the local OpenAI-compatible API boundary.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const TestPrompt = "Reply with the word ready."

// Base accepts an API root or an explicit versioned prefix, never an API action.
// Literal private LAN addresses require opt-in; localhost is pinned to loopback.
func Base(raw string, lan bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("Enter a local API URL, such as http://127.0.0.1:1234/v1.")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("Use an API base URL without credentials, query parameters or escaped paths.")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		port := u.Port()
		host = "127.0.0.1"
		u.Host = host
		if port != "" {
			u.Host = net.JoinHostPort(host, port)
		}
	}

	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !(lan && ip.IsPrivate())) {
		return "", errors.New("Use a loopback address. Explicit LAN mode accepts private IP addresses only.")
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/v1"
	}
	if !strings.HasSuffix(path, "/v1") || strings.Contains(path, "//") || strings.Contains(path, "..") {
		return "", errors.New("Enter the API base URL ending in /v1, not a chat page or completion route.")
	}
	u.Path = path
	return u.String(), nil
}

func Route(base, path string) string {
	return strings.TrimRight(base, "/") + strings.TrimPrefix(path, "/v1")
}

// Requests to local servers must never use an environment proxy or redirects.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Minute, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type Connection struct {
	URL      string
	Key      string
	AllowLAN bool
}
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{code, message} }
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "invalid_url"
}
func (c Connection) request(ctx context.Context, method, path string, payload any, timeout time.Duration) ([]byte, error) {
	base, err := Base(c.URL, c.AllowLAN)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, Route(base, path), body)
	if err != nil {
		return nil, failure("invalid_url", "The API address is invalid.")
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := Client(timeout)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, failure("unavailable", "The local server did not respond. Open your model app, enable its API server, and check the address.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, failure("authentication_required", "The local server requires a valid API key. This is separate from your Model Uplink key.")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, failure("upstream_error", "The local server rejected this request. Check its API settings and selected model.")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(b) > 2<<20 {
		return nil, failure("invalid_response", "The local server returned an unreadable response.")
	}
	return b, nil
}
func (c Connection) Models(ctx context.Context) ([]string, error) {
	b, err := c.request(ctx, "GET", "/v1/models", nil, 5*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &data) != nil || data.Data == nil {
		return nil, failure("invalid_response", "This address did not return an OpenAI-compatible model list.")
	}
	models := []string{}
	seen := map[string]bool{}
	for _, m := range *data.Data {
		if m.ID != "" && len(m.ID) <= 200 && !seen[m.ID] {
			models = append(models, m.ID)
			seen[m.ID] = true
		}
	}
	return models, nil
}
func (c Connection) Test(ctx context.Context, model string) error {
	if model == "" {
		return failure("model_required", "Choose a model to test.")
	}
	b, err := c.request(ctx, "POST", "/v1/chat/completions", map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": TestPrompt}}, "max_tokens": 512, "stream": false}, 90*time.Second)
	if err != nil {
		return err
	}
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(b, &reply) != nil || len(reply.Choices) == 0 || strings.TrimSpace(reply.Choices[0].Message.Content) == "" {
		return failure("no_output", "The model did not produce a chat response. Check it in your model app and try again.")
	}
	return nil
}
