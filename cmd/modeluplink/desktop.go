package main

// The native app uses one request/response over anonymous pipes. Credentials
// never enter command-line arguments, a local HTTP listener, or diagnostic logs.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"

	"github.com/oscar-investmatic/modeluplink-client/internal/engine"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/upstream"
	"github.com/oscar-investmatic/modeluplink-client/pkg/agent"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
	"github.com/oscar-investmatic/modeluplink-client/pkg/naming"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

type desktopRequest struct {
	LocalURL     string `json:"local_url"`
	LocalKey     string `json:"local_key"`
	MoveTrialID  string `json:"move_trial_id"`
	EndpointName string `json:"endpoint_name"`
	Action       string `json:"action"`
	Enabled      string `json:"enabled"`
	Email        string `json:"email"`
	Challenge    string `json:"challenge_id"`
	Code         string `json:"code"`
	Model        string `json:"model"`
	Models       string `json:"shared_models"`
	ModelIDs     string `json:"model_ids"`
	Slug         string `json:"slug"`
}

type desktopResponse struct {
	Source               *localServer              `json:"source,omitempty"`
	Servers              []localServer             `json:"servers,omitempty"`
	LocalSources         map[string]localServer    `json:"local_sources,omitempty"`
	StartupEnabled       *bool                     `json:"startup_enabled,omitempty"`
	Retired              []client.Endpoint         `json:"retired,omitempty"`
	OtherTrial           *client.Endpoint          `json:"other_trial,omitempty"`
	ConnectionLimit      *client.Endpoint          `json:"connection_limit,omitempty"`
	Stopped              map[string]bool           `json:"stopped,omitempty"`
	MemoryReleasePending map[string]bool           `json:"memory_release_pending,omitempty"`
	SharedModels         map[string][]string       `json:"shared_models,omitempty"`
	Activity             map[string]agent.Activity `json:"activity,omitempty"`
	Unavailable          bool                      `json:"unavailable,omitempty"`
	Error                string                    `json:"error,omitempty"`
	Notice               string                    `json:"notice,omitempty"`
	Challenge            string                    `json:"challenge_id,omitempty"`
	Account              *client.Account           `json:"account,omitempty"`
	Endpoints            []client.Endpoint         `json:"endpoints"`
	Models               []string                  `json:"models"`
	Recommendation       string                    `json:"recommendation,omitempty"`
	NameSuggestions      []string                  `json:"name_suggestions,omitempty"`
	Endpoint             *client.Endpoint          `json:"endpoint,omitempty"`
	APIKey               string                    `json:"api_key,omitempty"`
	Ready                bool                      `json:"ready"`
}

func desktopCommand() error {
	output := os.Stdout
	// Existing setup diagnostics go to the app's discarded stderr pipe.
	// Only structured progress and the final result go to stdout.
	os.Stdout = os.Stderr
	defer func() { os.Stdout = output }()
	var request desktopRequest
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return json.NewEncoder(output).Encode(desktopResponse{Error: "The app could not read this request. Please try again."})
	}
	encoder := json.NewEncoder(output)
	response, err := desktopDispatchWithProgress(request, func(p setupProgress) { _ = encoder.Encode(p) })
	if err != nil {
		response.Error = desktopError(err)
	}
	return json.NewEncoder(output).Encode(response)
}

func desktopDispatch(request desktopRequest) (desktopResponse, error) {
	return desktopDispatchWithProgress(request, nil)
}

func desktopDispatchWithProgress(request desktopRequest, report setupReporter) (desktopResponse, error) {
	var out desktopResponse
	if request.Action == "discover_servers" || request.Action == "inspect_source" || request.Action == "test_source" {
		return desktopInspect(request)
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return out, errors.New("Your saved settings could not be opened. Please contact support.")
	}
	api := client.New(envOr("MODELUPLINK_CONTROL_URL", valueOr(cfg.ControlURL, defaultControlURL)), cfg.AccountToken)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch request.Action {
	case "start_code":
		out.Challenge, err = api.StartEmailCode(ctx, strings.TrimSpace(request.Email))
		return out, err
	case "verify_code":
		token, account, verifyErr := api.VerifyEmailCode(ctx, request.Challenge, request.Code)
		if verifyErr != nil {
			return out, verifyErr
		}
		if token == "" {
			return out, errors.New("Sign-in could not be completed. Request a new code.")
		}
		cfg.ControlURL, cfg.AccountToken = api.BaseURL, token
		if err = localconfig.Save(cfg); err != nil {
			_ = client.New(api.BaseURL, token).SignOut(ctx)
			return out, errors.New("Your sign-in could not be saved on this Mac. Please try again.")
		}
		state, stateErr := desktopDispatch(desktopRequest{Action: "state"})
		if stateErr != nil {
			// Authentication has already succeeded and consumed the code.
			// A subsequent status outage must not send the user back to it.
			out.Account = &account
			out.Unavailable = true
			out.Notice = "You’re signed in. We couldn’t load your connections yet; tap Refresh to try again."
			return out, nil
		}
		return state, nil
	case "startup":
		enabled, parseErr := strconv.ParseBool(request.Enabled)
		if parseErr != nil {
			return out, errors.New("Choose whether sharing starts at sign-in.")
		}
		if err = service.SetStartup(enabled); err != nil {
			return out, err
		}
		previousEnabled := !cfg.StartupDisabled
		cfg.StartupDisabled = !enabled
		if err = localconfig.Save(cfg); err != nil {
			_ = service.SetStartup(previousEnabled)
			return out, err
		}
		out.StartupEnabled = &enabled
		return out, nil
	case "stop_all":
		out.Stopped = map[string]bool{}
		out.MemoryReleasePending = map[string]bool{}
		stopFailed, releasePending := false, false
		for _, e := range cfg.Endpoints {
			cfg, err = localconfig.Load()
			if err != nil {
				stopFailed = true
				break
			}
			if p, readErr := localconfig.EndpointPath(e.Slug); readErr == nil {
				if saved, loadErr := localconfig.LoadEndpoint(p); loadErr == nil {
					e = saved
				}
			}
			reply, stopErr := desktopStopSharing(cfg, e, report)
			if stopErr != nil {
				stopFailed = true
				continue
			}
			out.Stopped[e.Slug] = reply.Stopped[e.Slug]
			out.MemoryReleasePending[e.Slug] = reply.MemoryReleasePending[e.Slug]
			releasePending = releasePending || reply.MemoryReleasePending[e.Slug]
		}
		if stopFailed {
			out.Notice = "We couldn’t finish stopping every connection. Choose Stop sharing to try again."
		} else if releasePending {
			out.Notice = "Your sharing has stopped, but Ollama hasn’t confirmed that model memory was released. Close other apps using the model, then choose Release model memory."
		}
		return out, nil

	case "state":
		enabled := !cfg.StartupDisabled
		out.StartupEnabled = &enabled
		// Model discovery must not delay the account's cross-computer choice.
		probeModels := func() {
			probeCtx, stopProbe := context.WithTimeout(ctx, 2*time.Second)
			defer stopProbe()
			out.Models, _ = engine.Probe(probeCtx, "http://127.0.0.1:11434")
		}
		out.Recommendation = "llama3.2:3b"
		if cfg.AccountToken == "" {
			probeModels()
			return out, nil
		}
		account, meErr := api.Me(ctx)
		var apiErr *client.APIError
		if errors.As(meErr, &apiErr) && apiErr.Status == 401 {
			return out, nil
		}
		if meErr != nil {
			return out, meErr
		}
		out.Account = &account
		if entitled(account) {
			out.NameSuggestions = naming.PaidSuggestions(account.ID)
		}
		remote, listErr := api.ListEndpoints(ctx)
		if listErr != nil {
			return out, listErr
		}
		out.OtherTrial = otherComputerTrial(account, remote, cfg)
		out.ConnectionLimit = paidConnectionLimit(account, remote)
		for _, endpoint := range remote {
			if local, ok := cfg.Endpoints[endpoint.Slug]; ok && local.ID == endpoint.ID {
				if path, pathErr := localconfig.EndpointPath(local.Slug); pathErr == nil {
					if saved, readErr := localconfig.LoadEndpoint(path); readErr == nil && saved.ID == local.ID {
						local = saved
					}
				}
				out.Endpoints = append(out.Endpoints, endpoint)
				if out.SharedModels == nil {
					out.SharedModels = map[string][]string{}
					out.Activity = map[string]agent.Activity{}
				}
				out.SharedModels[endpoint.Slug] = local.SharedModels
				if out.LocalSources == nil {
					out.LocalSources = map[string]localServer{}
				}
				out.LocalSources[endpoint.Slug] = inspectEndpoint(ctx, local)
				if out.Stopped == nil {
					out.Stopped = map[string]bool{}
					out.MemoryReleasePending = map[string]bool{}
				}
				out.Stopped[endpoint.Slug] = local.Stopped
				out.MemoryReleasePending[endpoint.Slug] = local.MemoryReleasePending
				path, _ := localconfig.EndpointPath(local.Slug)
				if bytes, readErr := os.ReadFile(path + ".activity"); readErr == nil {
					var activity agent.Activity
					if json.Unmarshal(bytes, &activity) == nil && time.Since(activity.UpdatedAt) < 10*time.Second {
						out.Activity[endpoint.Slug] = activity
					}
				}
			}
		}
		// A revoked agent writes its own endpoint file, never the shared account
		// config. Retain a memory-release action if Ollama could not confirm unload.
		for _, local := range cfg.Endpoints {
			path, _ := localconfig.EndpointPath(local.Slug)
			if saved, readErr := localconfig.LoadEndpoint(path); readErr == nil && saved.ID == local.ID && saved.Revoked && saved.MemoryReleasePending {
				out.Retired = append(out.Retired, client.Endpoint{ID: saved.ID, Slug: saved.Slug})
			}
		}
		probeModels()
		sort.Slice(out.Endpoints, func(i, j int) bool { return out.Endpoints[i].Slug < out.Endpoints[j].Slug })
		return out, nil
	case "sign_out":
		if err = api.SignOut(ctx); err != nil {
			return out, err
		}
		cfg.AccountToken = ""
		return out, localconfig.Save(cfg)
	}
	if cfg.AccountToken == "" {
		return out, errors.New("Sign in to connect this Mac.")
	}
	if request.Action == "connect" || request.Action == "move_trial" {
		if strings.TrimSpace(request.Model) == "" || len(request.Model) > 200 || strings.HasPrefix(request.Model, "-") {
			return out, errors.New("Choose a model first.")
		}
		report.stage("account", "Checking your account…")
		// Do not open a checkout unexpectedly during a model download.
		account, meErr := api.Me(ctx)
		if meErr != nil {
			return out, meErr
		}
		if !entitled(account) && account.TrialStatus != "available" && account.TrialStatus != "active" {
			return out, errors.New("Your trial has ended. Open your dashboard to subscribe, then connect again.")
		}
		endpointName := ""
		if entitled(account) {
			endpointName = naming.NormalizeMissionName(request.EndpointName)
			if endpointName == "" {
				endpointName = naming.PaidSuggestions(account.ID)[0]
			}
			if nameErr := security.ValidateSlug(endpointName); nameErr != nil {
				return out, errors.New("Choose an endpoint name with 3–48 letters, numbers, or hyphen-separated words.")
			}
		}
		remote, listErr := api.ListEndpoints(ctx)
		if listErr != nil {
			return out, listErr
		}
		other := otherComputerTrial(account, remote, cfg)
		if paidConnectionLimit(account, remote) != nil {
			return out, errors.New(connectionLimitMessage)
		}
		if request.Action == "move_trial" {
			if other == nil || request.MoveTrialID == "" || other.ID != request.MoveTrialID {
				return out, errors.New("Your trial connection changed. Refresh before moving it.")
			}
		} else if other != nil {
			return out, errors.New("Your trial is already sharing a model from another computer. Choose whether to keep using it or move sharing here.")
		}
		// A failed readiness check leaves a working background agent. Return its
		// address/key so the app never loses credentials or creates a duplicate.
		stage := "prepare"
		selected, selectionErr := desktopModels(request)
		if selectionErr != nil {
			return out, selectionErr
		}
		args := []string{"ollama", "--model", selected[0]}
		if request.LocalURL != "" {
			args = []string{"openai_compatible", "--url", request.LocalURL, "--model", selected[0]}
		}
		if endpointName != "" {
			args = append(args, "--name", endpointName)
		}
		for _, model := range selected[1:] {
			args = append(args, "--share-model", model)
		}
		moveID := ""
		if request.Action == "move_trial" {
			moveID = request.MoveTrialID
		}
		err = serveWithCredentials(args, func(e localconfig.Endpoint, key string, ready bool) {
			out.Endpoint = &client.Endpoint{ID: e.ID, Slug: e.Slug, URL: e.URL, Engine: e.Engine, Region: e.Region, Trial: e.Trial, Online: ready}
			out.APIKey, out.Ready = key, ready
			out.SharedModels = map[string][]string{e.Slug: e.SharedModels}
			out.LocalSources = map[string]localServer{e.Slug: inspectEndpoint(ctx, e)}
		}, func(p setupProgress) {
			stage = p.Stage
			if report != nil {
				report(p)
			}
		}, moveID, request.LocalKey)
		if out.Endpoint != nil && moveID != "" {
			out.Notice = "Sharing has moved here. Use the new address and API key. Your remaining trial allowance is unchanged."
		}
		if out.Endpoint != nil && err != nil {
			out.Notice = "Your address is reserved. We’re still connecting and will keep trying in the background."
			return out, nil
		}
		if err != nil {
			if request.LocalURL != "" {
				return out, err
			}
			return out, setupError(stage, err)
		}
		return out, nil
	}
	local, ok := cfg.Endpoints[request.Slug]
	if !ok || local.Slug != request.Slug {
		return out, errors.New("This connection isn’t configured on this Mac. Refresh and try again.")
	}
	if path, pathErr := localconfig.EndpointPath(local.Slug); pathErr == nil {
		if saved, readErr := localconfig.LoadEndpoint(path); readErr == nil && saved.ID == local.ID {
			local = saved
		}
	}
	if local.Revoked && request.Action != "pause" && request.Action != "delete" {
		return out, errors.New("This connection has moved or been removed. Refresh to see your current sharing options.")
	}
	switch request.Action {
	case "share_models":
		return desktopShareModels(request, cfg, local, report)
	case "pause":
		return desktopStopSharing(cfg, local, report)
	case "resume":
		path, pathErr := localconfig.EndpointPath(local.Slug)
		if pathErr != nil {
			return out, pathErr
		}
		executable, exeErr := os.Executable()
		if exeErr != nil {
			return out, exeErr
		}
		previous := local
		local.Stopped = false
		local.MemoryReleasePending = false
		if _, err = localconfig.SaveEndpoint(local); err != nil {
			return out, err
		}
		_, err = installService(executable, path, local.Slug)
		if err != nil {
			_, _ = localconfig.SaveEndpoint(previous)
			return out, err
		}
		cfg.Endpoints[local.Slug] = local
		err = localconfig.Save(cfg)

	case "delete":
		err = deleteEndpoint([]string{local.Slug, "--yes"})
	case "new_key":
		_, out.APIKey, err = api.CreateKey(ctx, local.ID, "Mac app")
	default:
		err = errors.New("This action isn’t available. Please update the app.")
	}
	return out, err
}

func desktopError(err error) string {
	var permissionError *flatpak.UserError
	if errors.As(err, &permissionError) {
		return permissionError.Message
	}
	var sourceErr *upstream.Error
	if errors.As(err, &sourceErr) {
		return sourceErr.Message
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 409:
			if apiErr.Code == "endpoint_limit_reached" {
				return connectionLimitMessage
			}
			if apiErr.Code == "endpoint_name_taken" {
				return "This address is already taken. Choose another name and try again."
			}
			if apiErr.Code == "trial_endpoint_exists" || apiErr.Code == "trial_move_changed" || apiErr.Code == "entitlement_changed" || apiErr.Code == "trial_move_unavailable" {
				return "Your trial connection changed. Refresh and choose where to share your model."
			}
		case 401:
			return "Your sign-in has expired. Sign in again to continue."
		case 404:
			return "This feature isn’t available on the server yet. Please try again after the update."
		case 429:
			return "A few too many attempts. Wait a little before trying again."
		case 400, 422:
			return apiErr.Message
		}
		return "Model Uplink couldn’t be reached. Please try again shortly."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "That took too long. Check your internet connection and try again."
	}
	// Only messages authored here are shown; transport and service errors can
	// contain filesystem paths or developer-facing instructions.
	if strings.HasPrefix(err.Error(), "Your ") || strings.HasPrefix(err.Error(), "Sign ") || strings.HasPrefix(err.Error(), "Choose ") || strings.HasPrefix(err.Error(), "We ") || strings.HasPrefix(err.Error(), "This ") || strings.HasPrefix(err.Error(), "Unlock ") || strings.HasPrefix(err.Error(), "Enter ") || strings.HasPrefix(err.Error(), "Use ") {
		return err.Error()
	}
	return "Something interrupted the connection. Check your internet connection and try again."
}
