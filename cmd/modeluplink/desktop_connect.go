package main

import (
	"context"
	"sync"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/upstream"
)

type localServer struct {
	URL       string   `json:"url"`
	Status    string   `json:"status"`
	Message   string   `json:"message,omitempty"`
	Models    []string `json:"models"`
	Ownership string   `json:"ownership"`
}

func inspectSource(ctx context.Context, c upstream.Connection) localServer {
	info := localServer{Ownership: "external", Models: []string{}}
	base, err := upstream.Base(c.URL, c.AllowLAN)
	if err == nil {
		info.URL = base
		info.Models, err = c.Models(ctx)
	}
	if err != nil {
		info.Status = upstream.Code(err)
		info.Message = err.Error()
		return info
	}
	info.Status = "server_available"
	info.Message = "API reachable. Test a selected model to check its response."
	if len(info.Models) == 0 {
		info.Status = "model_unavailable"
		info.Message = "No models were found. Add a model in your runtime app."
	}
	return info
}
func inspectEndpoint(ctx context.Context, e localconfig.Endpoint) localServer {
	info := localServer{URL: e.UpstreamURL, Ownership: e.RuntimeOwnership, Models: []string{}}
	if e.ManagesRuntime() {
		info.Ownership = "managed"
	} else {
		info.Ownership = "external"
	}
	if e.Stopped || e.Revoked {
		info.Models = e.SharedModels
		info.Status = "stopped"
		info.Message = "Remote sharing is stopped."
		return info
	}
	key, err := localconfig.ResolveUpstreamKey(e)
	if err != nil {
		info.Status = "credential_store_locked"
		info.Message = err.Error()
		return info
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	probed := inspectSource(probeCtx, upstream.Connection{URL: e.UpstreamURL, Key: key, AllowLAN: e.AllowLAN})
	probed.Ownership = info.Ownership
	if probed.Status == "server_available" {
		for _, m := range e.SharedModels {
			if !contains(probed.Models, m) {
				probed.Status = "model_unavailable"
				probed.Message = "A shared model is unavailable. Open your runtime app or update the shared selection."
				break
			}
		}
	}
	return probed
}
func desktopInspect(request desktopRequest) (desktopResponse, error) {
	out := desktopResponse{}
	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Second)
	defer cancel()
	if request.Action == "discover_servers" {
		// A bounded local scan, requested by the user. Port numbers are not identities.
		ports := []string{"1234", "11434", "8080", "8000", "1337"}
		results := make([]localServer, len(ports))
		var wg sync.WaitGroup
		for i, p := range ports {
			wg.Add(1)
			go func() {
				defer wg.Done()
				probeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				defer stop()
				results[i] = inspectSource(probeCtx, upstream.Connection{URL: "http://127.0.0.1:" + p + "/v1"})
			}()
		}
		wg.Wait()
		out.Servers = []localServer{}
		for _, r := range results {
			if r.Status == "server_available" || r.Status == "model_unavailable" || r.Status == "authentication_required" {
				out.Servers = append(out.Servers, r)
			}
		}
		return out, nil
	}
	source := upstream.Connection{URL: request.LocalURL, Key: request.LocalKey}
	if request.Slug != "" {
		cfg, err := localconfig.Load()
		if err != nil {
			return out, err
		}
		e, ok := cfg.Endpoints[request.Slug]
		if !ok {
			return out, &upstream.Error{Code: "unknown_connection", Message: "This connection is not configured on this computer."}
		}
		key, err := localconfig.ResolveUpstreamKey(e)
		if err != nil {
			return out, err
		}
		source = upstream.Connection{URL: e.UpstreamURL, Key: key, AllowLAN: e.AllowLAN}
	}
	info := inspectSource(ctx, source)
	out.Source = &info
	out.Models = info.Models
	if info.Status != "server_available" {
		return out, nil
	}
	if request.Action == "test_source" {
		if !contains(info.Models, request.Model) {
			info.Status = "model_unavailable"
			info.Message = "Choose a model returned by this server."
			return out, nil
		}
		if err := source.Test(ctx, request.Model); err != nil {
			info.Status = upstream.Code(err)
			info.Message = err.Error()
		} else {
			info.Status = "ready"
			info.Message = "The selected model answered the local test. Other capabilities remain unverified."
			out.Ready = true
		}
	}
	return out, nil
}
