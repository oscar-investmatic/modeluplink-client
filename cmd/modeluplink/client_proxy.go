package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"

	mtunnel "github.com/oscar-investmatic/modeluplink-client/pkg/tunnel"
)

type clientCredentials struct {
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
	TLSPin   string `json:"tls_pin"`
}

func clientCommand(args []string) error {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	credentialsPath := fs.String("credentials-file", "", "mode-0600 JSON file containing endpoint, api_key, and tls_pin")
	listenAddress := fs.String("listen", "127.0.0.1:11435", "loopback address for the local OpenAI-compatible proxy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *credentialsPath == "" {
		return errors.New("--credentials-file is required")
	}
	if err := validateLoopbackListen(*listenAddress); err != nil {
		return err
	}
	credentials, err := loadClientCredentials(*credentialsPath)
	if err != nil {
		return err
	}
	handler, err := newClientProxyHandler(credentials, nil)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Println("Verified local base URL: http://" + listener.Addr().String() + "/v1")
	fmt.Println("The remote endpoint certificate chain, hostname, and paired TLS pin are all required.")
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	}
}

func loadClientCredentials(path string) (clientCredentials, error) {
	info, err := os.Stat(path)
	if err != nil {
		return clientCredentials{}, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return clientCredentials{}, errors.New("credentials file must not be accessible by group or other users; run chmod 600 on it")
	}
	file, err := os.Open(path)
	if err != nil {
		return clientCredentials{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return clientCredentials{}, err
	}
	data, err = localconfig.UnprotectSecrets(data)
	if err != nil {
		return clientCredentials{}, err
	}
	var credentials clientCredentials
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&credentials); err != nil {
		return clientCredentials{}, fmt.Errorf("read credentials file: %w", err)
	}
	if strings.TrimSpace(credentials.APIKey) == "" {
		return clientCredentials{}, errors.New("credentials file api_key is required")
	}
	return credentials, nil
}

func saveClientCredentials(path string, credentials clientCredentials) error {
	payload, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	payload, err = localconfig.ProtectSecrets(payload)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func newClientProxyHandler(credentials clientCredentials, roots *x509.CertPool) (http.Handler, error) {
	endpoint, err := url.Parse(credentials.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("credentials endpoint must be an https URL without credentials, query, or fragment")
	}
	if strings.TrimRight(endpoint.Path, "/") != "/v1" {
		return nil, errors.New("credentials endpoint must end in /v1")
	}
	expectedPin, err := parseTLSPin(credentials.TLSPin)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: endpoint.Hostname(),
		RootCAs:    roots,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("endpoint did not provide a certificate")
			}
			actual := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(actual[:], expectedPin) != 1 {
				return errors.New("endpoint TLS pin mismatch; refusing to send inference data")
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		TLSClientConfig:       tlsConfig,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = endpoint.Scheme
			request.Out.URL.Host = endpoint.Host
			request.Out.Host = endpoint.Host
			request.Out.Header = make(http.Header)
			mtunnel.ApplyHeaders(request.Out.Header, mtunnel.RequestHeaders(request.In.Header))
			request.Out.Header.Set("Authorization", "Bearer "+credentials.APIKey)
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
			message := "verified endpoint connection failed"
			if strings.Contains(proxyErr.Error(), "TLS pin mismatch") {
				message = "endpoint TLS pin mismatch"
			}
			writeClientProxyError(w, http.StatusBadGateway, "verified_connection_failed", message)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeClientProxyError(w, http.StatusForbidden, "origin_not_allowed", "browser origins are disabled on the local proxy")
			return
		}
		if !clientProxyRouteAllowed(r.Method, r.URL.Path) {
			writeClientProxyError(w, http.StatusNotFound, "route_not_found", "this route is not exposed by Model Uplink")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		proxy.ServeHTTP(w, r)
	}), nil
}

func parseTLSPin(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "sha256/") {
		return nil, errors.New("tls_pin must use sha256/<base64> format")
	}
	pin, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "sha256/"))
	if err != nil || len(pin) != sha256.Size {
		return nil, errors.New("tls_pin must contain a base64-encoded SHA-256 digest")
	}
	return pin, nil
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("--listen must include a loopback host and port")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("verified client proxy must listen on loopback")
	}
	return nil
}

func clientProxyRouteAllowed(method, path string) bool {
	switch method + " " + path {
	case "GET /v1/models", "POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses", "POST /v1/embeddings":
		return true
	default:
		return false
	}
}

func writeClientProxyError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "type": "modeluplink_error", "code": code}})
}
