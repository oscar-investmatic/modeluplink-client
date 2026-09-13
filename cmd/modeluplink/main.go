package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/buildinfo"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"

	"github.com/oscar-investmatic/modeluplink-client/internal/engine"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"
	upstreamapi "github.com/oscar-investmatic/modeluplink-client/internal/upstream"
	"github.com/oscar-investmatic/modeluplink-client/pkg/agent"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
	"github.com/oscar-investmatic/modeluplink-client/pkg/naming"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

const defaultControlURL = "https://api.modeluplink.com"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "modeluplink:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	if handled, err := platformCommand(args[0]); handled {
		return err
	}
	switch args[0] {
	case "login":
		return login(args[1:])
	case "connect":
		return serve(append([]string{"openai_compatible"}, args[1:]...))
	case "serve":
		return serve(args[1:])
	case "status":
		return status(args[1:])
	case "logs":
		return logs(args[1:])
	case "stop":
		return stop(args[1:])
	case "delete":
		return deleteEndpoint(args[1:])
	case "upgrade":
		return upgrade(args[1:])
	case "key":
		return keys(args[1:])
	case "pin":
		return pin(args[1:])
	case "client":
		return clientCommand(args[1:])
	case "engine":
		return engineCommand(args[1:])
	case "_flatpak":
		return flatpak.Run(len(args) > 1 && args[1] == "--background")
	case "_desktop":
		return desktopCommand()
	case "_managed-ollama":
		return service.RunManagedOllama(context.Background())
	case "_agent":
		return agentCommand(args[1:])
	case "version", "--version", "-v":
		if len(args) > 1 && args[1] == "--json" {
			info := buildinfo.Current()
			return json.NewEncoder(os.Stdout).Encode(struct {
				Version string `json:"version"`
				buildinfo.Info
			}{agent.Version, info})
		}
		fmt.Println(agent.Version)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q; run modeluplink help", args[0])
	}
}

func usage() {
	fmt.Print(`Model Uplink publishes local models through a permanent OpenAI-compatible URL.

Usage:
  modeluplink login
  modeluplink connect --url URL --model MODEL [--share-model MODEL] [--upstream-key-env VARIABLE]
  modeluplink serve ollama [--name NAME] [--model MODEL] [--region auto|eu|us] [--cors-origin ORIGIN]
  modeluplink serve vllm --url URL [--name NAME] [--region auto|eu|us] [--cors-origin ORIGIN]
  modeluplink status
  modeluplink logs ENDPOINT [--follow]
  modeluplink stop ENDPOINT
  modeluplink delete ENDPOINT [--yes]
  modeluplink upgrade
  modeluplink key create ENDPOINT --name NAME
  modeluplink key list ENDPOINT
  modeluplink key revoke KEY_ID
  modeluplink pin ENDPOINT
  modeluplink client --credentials-file FILE [--listen 127.0.0.1:11435]
  modeluplink engine update ollama
`)
}

func login(args []string) error {
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	controlURL := fs.String("control-url", envOr("MODELUPLINK_CONTROL_URL", valueOr(cfg.ControlURL, defaultControlURL)), "control API URL")
	token := fs.String("token", os.Getenv("MODELUPLINK_ACCOUNT_TOKEN"), "existing account session token")
	email := fs.String("email", "", "email address for passwordless login")
	if err = fs.Parse(args); err != nil {
		return err
	}
	api := client.New(*controlURL, "")
	if *token == "" {
		if *email == "" {
			fmt.Print("Email: ")
			line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
			if readErr != nil && !errors.Is(readErr, os.ErrClosed) {
				return readErr
			}
			*email = strings.TrimSpace(line)
		}
		device, startErr := api.StartDeviceAuthorization(context.Background(), *email)
		if startErr != nil {
			return fmt.Errorf("start passwordless login: %w", startErr)
		}
		fmt.Println("Check", *email, "for a Model Uplink email titled \"Approve a command-line sign-in\".")
		if device.UserCode != "" {
			fmt.Println("On the approval page, enter this code to confirm it is this terminal:")
			fmt.Println()
			fmt.Println("    " + device.UserCode)
			fmt.Println()
			fmt.Println("The email shows the same code. Do not approve a request whose code differs.")
		}
		if device.DevelopmentVerificationURL != "" {
			fmt.Println("Development link:", device.DevelopmentVerificationURL)
			_ = openBrowser(device.DevelopmentVerificationURL)
		}
		expires := time.Duration(device.ExpiresIn) * time.Second
		if expires <= 0 {
			expires = 10 * time.Minute
		}
		interval := time.Duration(device.Interval) * time.Second
		if interval < time.Second {
			interval = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), expires)
		defer cancel()
		for {
			value, pending, pollErr := api.PollDeviceAuthorization(ctx, device.DeviceCode)
			if pollErr != nil {
				return fmt.Errorf("complete passwordless login: %w", pollErr)
			}
			if !pending {
				*token = value
				break
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.New("login expired; run modeluplink login again")
			case <-timer.C:
			}
		}
	}
	api = client.New(*controlURL, *token)
	if _, err = api.ListEndpoints(context.Background()); err != nil {
		return fmt.Errorf("verify login: %w", err)
	}
	cfg.ControlURL = *controlURL
	cfg.AccountToken = *token
	if err = localconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Logged in to", *controlURL)
	return nil
}

// Process-level seams replaced by tests; production keeps the real commands.
var (
	openBrowser          = openBrowserCommand
	installService       = service.Install
	uninstallService     = service.Uninstall
	waitForReady         = waitForEndpoint
	checkoutPollInterval = 2 * time.Second
)

func openBrowserCommand(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		return openWindowsBrowser(target)
	case "darwin":
		cmd = hostexec.Command("open", target)
	case "linux":
		cmd = hostexec.Command("xdg-open", target)
	default:
		return nil
	}
	return cmd.Start()
}

func serve(args []string) error {
	return serveWithResult(args, nil)
}

func serveWithResult(args []string, result func(localconfig.Endpoint, string, bool)) error {
	return serveWithProgress(args, result, nil)
}

func serveWithProgress(args []string, result func(localconfig.Endpoint, string, bool), report setupReporter) error {
	return serveWithMove(args, result, report, "")
}

// moveTrialID is supplied only after the desktop's explicit confirmation.
func serveWithMove(args []string, result func(localconfig.Endpoint, string, bool), report setupReporter, moveTrialID string) error {
	return serveWithCredentials(args, result, report, moveTrialID, "")
}
func serveWithCredentials(args []string, result func(localconfig.Endpoint, string, bool), report setupReporter, moveTrialID, providedKey string) error {
	if len(args) == 0 {
		return errors.New("usage: modeluplink serve ollama|vllm")
	}
	kind := args[0]
	if kind != "ollama" && kind != "vllm" && kind != "openai_compatible" {
		return errors.New("engine must be ollama, vllm or openai_compatible")
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve "+kind, flag.ContinueOnError)
	name := fs.String("name", "", "permanent endpoint name (globally unique)")
	displayName := fs.String("display-name", "", "dashboard display name")
	model := fs.String("model", "", "model ID to prepare and share")
	var sharedModels stringList
	fs.Var(&sharedModels, "share-model", "additional model ID to share; may be repeated")
	region := fs.String("region", "auto", "relay region: auto, eu, or us")
	upstream := fs.String("url", "", "local inference server base URL")
	foreground := fs.Bool("foreground", false, "keep the tunnel attached to this terminal")
	noInstall := fs.Bool("no-install", false, "do not install Ollama when missing")
	allowLAN := fs.Bool("allow-lan-upstream", false, "allow a non-loopback upstream URL")
	upstreamKeyEnv := fs.String("upstream-key-env", "", "environment variable containing the local server API key")
	controlURL := fs.String("control-url", envOr("MODELUPLINK_CONTROL_URL", valueOr(cfg.ControlURL, defaultControlURL)), "control API URL")
	accountToken := fs.String("account-token", envOr("MODELUPLINK_ACCOUNT_TOKEN", cfg.AccountToken), "account session token")
	relayOverride := fs.String("relay-url", "", "override the relay URL returned by the control plane")
	var corsOrigins stringList
	fs.Var(&corsOrigins, "cors-origin", "exact browser origin to allow; may be repeated")
	if err = fs.Parse(args[1:]); err != nil {
		return err
	}
	if *accountToken == "" {
		return errors.New("not logged in; run modeluplink login first")
	}
	if *region != "auto" && *region != "eu" && *region != "us" {
		return errors.New("region must be auto, eu, or us")
	}
	for i, origin := range corsOrigins {
		corsOrigins[i], err = security.NormalizeCORSOrigin(origin)
		if err != nil {
			return fmt.Errorf("invalid --cors-origin %q: %w", origin, err)
		}
	}
	if _, exists := cfg.Endpoints[*name]; *name != "" && exists {
		return fmt.Errorf("endpoint %q already exists on this machine", *name)
	}
	if *upstream == "" {
		switch kind {
		case "ollama":
			*upstream = "http://127.0.0.1:11434"
		case "vllm":
			*upstream = "http://127.0.0.1:8000"
		default:
			return errors.New("Enter the local API URL with --url.")
		}
	}
	if err = agent.ValidateUpstream(*upstream, *allowLAN); err != nil {
		return err
	}
	if kind == "openai_compatible" {
		*upstream, err = upstreamapi.Base(*upstream, *allowLAN)
		if err != nil {
			return err
		}
	}
	upstreamKey := providedKey
	if *upstreamKeyEnv != "" {
		upstreamKey = os.Getenv(*upstreamKeyEnv)
		if upstreamKey == "" {
			return errors.New("The local server API key environment variable is empty.")
		}
	}
	source := upstreamapi.Connection{URL: *upstream, Key: upstreamKey, AllowLAN: *allowLAN}
	var ollamaPath string
	if kind == "ollama" {
		report.stage("prepare", "Checking your Mac and starting Ollama…")
		hardware := engine.DetectHardware()
		fmt.Println("Detected:", hardware.Summary())
		if *upstream == "" {
			*upstream = "http://127.0.0.1:11434"
		}
		ollamaPath, err = engine.EnsureOllama(context.Background(), !*noInstall)
		if err != nil {
			return err
		}
		models, probeErr := engine.Probe(context.Background(), *upstream)
		if probeErr != nil {
			return fmt.Errorf("probe Ollama: %w", probeErr)
		}
		if *model != "" && !contains(models, *model) {
			report.stage("download", "Getting your model’s download details…")
			if report != nil {
				err = engine.PullModelWithProgress(context.Background(), *upstream, *model, func(p engine.PullProgress) {
					message := "Downloading model file…"
					if p.Total == 0 {
						message = "Preparing model files…"
					}
					if p.Status == "verifying sha256 digest" {
						message = "Verifying your download…"
					}
					if p.Status == "writing manifest" || p.Status == "success" {
						message = "Finishing model installation…"
					}
					report(setupProgress{Event: "progress", Stage: "download", Message: message, Completed: p.Completed, Total: p.Total})
				})
			} else {
				err = engine.PullModel(context.Background(), ollamaPath, *model)
			}
			if err != nil {
				return fmt.Errorf("pull model: %w", err)
			}
		} else if len(models) == 0 {
			if !interactive() {
				return errors.New("Ollama has no models; pass --model in non-interactive use")
			}
			chosen, chooseErr := engine.ChooseModelForHardware(os.Stdin, os.Stdout, hardware)
			if chooseErr != nil {
				return chooseErr
			}
			*model = chosen
			if err = engine.PullModel(context.Background(), ollamaPath, chosen); err != nil {
				return fmt.Errorf("pull model: %w", err)
			}
		}
		if *model == "" && len(models) > 0 {
			*model = models[0]
		}
		sharedModels = append([]string{*model}, sharedModels...)
		// Ollama accepts untagged names, but discovery and loaded-model status
		// report their canonical :latest IDs. Persist those IDs for both sharing
		// enforcement and unloading when sharing stops.
		for i, selected := range sharedModels {
			sharedModels[i] = canonicalOllamaModel(selected)
		}
		if err = engine.ConfigureManagedOllama(context.Background()); err != nil {
			return err
		}
		for _, selected := range sharedModels {
			report.stage("modelcheck", "Checking that "+selected+" can answer on this Mac…")
			if err = engine.CheckModel(context.Background(), *upstream, selected); err != nil {
				return fmt.Errorf("model readiness: %w", err)
			}
		}
	} else {
		report.stage("prepare", "Checking your local API server…")
		models, probeErr := source.Models(context.Background())
		if probeErr != nil {
			return probeErr
		}
		if kind == "openai_compatible" {
			if *model != "" {
				sharedModels = append([]string{*model}, sharedModels...)
			}
			if len(sharedModels) == 0 || len(sharedModels) > 8 {
				return errors.New("Choose one to eight models with --model and --share-model.")
			}
			for _, selected := range sharedModels {
				if !contains(models, selected) {
					return errors.New("A selected model is unavailable. Refresh the local server model list.")
				}
				report.stage("modelcheck", "Testing a short response from "+selected+"…")
				if err = source.Test(context.Background(), selected); err != nil {
					return err
				}
			}
		}
	}

	report.stage("reserve", "Your model is ready. Reserving its address…")
	selectedRegion := *region
	if selectedRegion == "auto" {
		selectedRegion = chooseRegion(*controlURL)
	}
	api := client.New(*controlURL, *accountToken)
	paid, trial, err := ensureEntitlement(api)
	if err != nil {
		return err
	}
	if trial {
		if *name != "" {
			fmt.Println("Free trial uses a temporary address; you choose the permanent name when you subscribe.")
		}
		*name = ""
	} else {
		if *name == "" {
			*name = defaultName()
		}
		if _, exists := cfg.Endpoints[*name]; exists {
			return fmt.Errorf("endpoint %q already exists on this machine", *name)
		}
		if *displayName == "" {
			*displayName = *name
		}
	}
	var created client.CreatedEndpoint
	if moveTrialID != "" {
		if !trial {
			return errors.New("Your account changed. Refresh before moving your trial.")
		}
		report.stage("reserve", "Moving sharing from your other computer…")
		created, err = api.MoveTrialEndpoint(context.Background(), moveTrialID, *displayName, kind, selectedRegion, corsOrigins)
	} else {
		created, err = api.CreateEndpoint(context.Background(), *name, *displayName, kind, selectedRegion, corsOrigins)
	}
	if err != nil {
		return fmt.Errorf("create endpoint: %w", err)
	}
	trialLine := ""
	if created.Endpoint.Trial || created.Trial {
		trialLine = trialServeLine(created.TrialRemaining, created.TrialTransferRemaining, created.TrialExpiresAt)
	}
	if paid {
		removeReplacedTrialEndpoints(&cfg, created.ReplacedTrialEndpointIDs)
	}
	relayURL := created.RelayURL
	if *relayOverride != "" {
		relayURL = *relayOverride
	}
	ownership := "external"
	if kind == "ollama" {
		ownership = "managed"
	}
	local := localconfig.Endpoint{RuntimeOwnership: ownership, SharedModels: sharedModels, ID: created.Endpoint.ID, Slug: created.Endpoint.Slug, Engine: kind, Region: created.Endpoint.Region, URL: created.Endpoint.URL, RelayURL: relayURL, ControlURL: *controlURL, AgentToken: created.AgentToken, UpstreamURL: *upstream, UpstreamKey: upstreamKey, AllowLAN: *allowLAN, CORSOrigins: created.Endpoint.CORSOrigins, Trial: trialLine != "", CreatedAt: time.Now().UTC()}
	endpointPath, err := localconfig.SaveEndpoint(local)
	if err != nil {
		_ = api.DeleteEndpoint(context.Background(), created.Endpoint.ID)
		return err
	}
	cfg.ControlURL = *controlURL
	cfg.AccountToken = *accountToken
	cfg.Endpoints[local.Slug] = local
	if err = localconfig.Save(cfg); err != nil {
		_ = api.DeleteEndpoint(context.Background(), created.Endpoint.ID)
		return err
	}
	if *foreground {
		return runForegroundAgent(local, created.APIKey, trialLine)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	report.stage("start", "Starting your background connection…")
	logLocation, err := installService(executable, endpointPath, local.Slug)
	if err != nil {
		_ = api.DeleteEndpoint(context.Background(), created.Endpoint.ID)
		delete(cfg.Endpoints, local.Slug)
		_ = localconfig.Save(cfg)
		return fmt.Errorf("install background service (endpoint rolled back): %w", err)
	}
	fmt.Println("Agent running in the background.")
	fmt.Println("Logs:", logLocation)
	report.stage("verify", "Checking your secure public connection. This can take a few minutes…")
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelReady()
	if err = waitForReady(readyCtx, created.Endpoint.URL, created.APIKey, nil); err != nil {
		if result != nil {
			result(local, created.APIKey, false)
		} else {
			printPendingCredentials(created.Endpoint.URL, created.APIKey, trialLine)
		}
		return fmt.Errorf("endpoint was created but is not ready yet: %w; the agent will keep retrying in the background", err)
	}
	if result != nil {
		result(local, created.APIKey, true)
	} else {
		printReadyCredentials(local, created.APIKey, trialLine)
	}
	return nil
}

func entitled(account client.Account) bool {
	return account.BillingState == "active" || account.BillingState == "grace"
}

// ensureEntitlement allows paid accounts and available or active trials to
// create an endpoint. It opens checkout only when neither allowance applies.
func ensureEntitlement(api *client.Client) (paid, trial bool, err error) {
	account, err := api.Me(context.Background())
	if err != nil {
		return false, false, fmt.Errorf("check billing: %w", err)
	}
	if entitled(account) {
		return true, false, nil
	}
	switch account.TrialStatus {
	case "available", "active":
		return false, true, nil
	case "exhausted", "expired":
		fmt.Println("Your free trial is over.")
	}
	if err = runCheckout(api); err != nil {
		return false, false, err
	}
	fmt.Println("Subscription active.")
	return true, false, nil
}

// runCheckout opens Stripe Checkout and polls until the account is entitled.
func runCheckout(api *client.Client) error {
	checkout, err := api.CreateCheckout(context.Background())
	if err != nil {
		return fmt.Errorf("start subscription checkout: %w", err)
	}
	fmt.Println("A paid endpoint subscription is required. Opening Stripe Checkout:")
	fmt.Println(checkout)
	_ = openBrowser(checkout)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for {
		timer := time.NewTimer(checkoutPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("checkout was not completed before the setup window expired")
		case <-timer.C:
		}
		account, err := api.Me(ctx)
		if err == nil && entitled(account) {
			return nil
		}
	}
}

func upgrade(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: modeluplink upgrade")
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	token := envOr("MODELUPLINK_ACCOUNT_TOKEN", cfg.AccountToken)
	if token == "" {
		return errors.New("not logged in; run modeluplink login first")
	}
	api := client.New(envOr("MODELUPLINK_CONTROL_URL", valueOr(cfg.ControlURL, defaultControlURL)), token)
	account, err := api.Me(context.Background())
	if err != nil {
		return fmt.Errorf("check billing: %w", err)
	}
	if !entitled(account) {
		if err = runCheckout(api); err != nil {
			return err
		}
	}
	fmt.Println("Subscription active. Run: modeluplink serve ollama --name NAME to reserve your permanent address (your trial endpoint will be replaced).")
	return nil
}

func trialDate(at *time.Time) string {
	if at == nil {
		return "unknown"
	}
	return at.Local().Format("Jan 2, 2006")
}

func trialTransfer(bytes int64) string {
	if bytes%1_000_000 == 0 {
		return fmt.Sprintf("%d MB", bytes/1_000_000)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/1_000_000)
}

func trialServeLine(remaining int, transferRemaining int64, expiresAt *time.Time) string {
	window := "starts on the first successful API call"
	if expiresAt != nil {
		window = "ends " + trialDate(expiresAt)
	}
	return fmt.Sprintf("Free trial: %d requests and %s transfer left; %s. Upgrade: modeluplink upgrade", remaining, trialTransfer(transferRemaining), window)
}

// removeReplacedTrialEndpoints forgets local trial endpoints the control
// plane deleted when a permanent endpoint was created. Failures only warn:
// the new endpoint is already live.
func removeReplacedTrialEndpoints(cfg *localconfig.Config, ids []string) {
	for _, id := range ids {
		for slug, local := range cfg.Endpoints {
			if local.ID != id {
				continue
			}
			if err := uninstallService(slug); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: trial endpoint %s was replaced, but its background service could not be removed: %v\n", slug, err)
			}
			if err := localconfig.DeleteEndpointFiles(slug); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: trial endpoint %s was replaced, but its local files could not be removed: %v\n", slug, err)
			}
			delete(cfg.Endpoints, slug)
			fmt.Println("Replaced trial endpoint", slug)
		}
	}
}

func printCredentials(url, key, pin, trialLine string) {
	fmt.Println()
	fmt.Println("Endpoint ready")
	fmt.Println("Base URL:", url)
	fmt.Println("API key: ", key)
	if pin != "" {
		fmt.Println("TLS pin: ", pin)
	}
	fmt.Println("The API key is shown once. Store it now.")
	if trialLine != "" {
		fmt.Println(trialLine)
	}
	fmt.Println()
	fmt.Printf("curl %s/models -H 'Authorization: Bearer %s'\n", url, key)
}

func printReadyCredentials(endpoint localconfig.Endpoint, key, trialLine string) {
	pin, err := endpointPin(endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: the endpoint is ready, but its local TLS pin could not be read:", err)
	}
	printCredentials(endpoint.URL, key, pin, trialLine)
}

func printPendingCredentials(url, key, trialLine string) {
	fmt.Println()
	fmt.Println("Endpoint created, but the public readiness check did not pass.")
	fmt.Println("Base URL:", url)
	fmt.Println("API key: ", key)
	fmt.Println("The API key is shown once. Store it now.")
	if trialLine != "" {
		fmt.Println(trialLine)
	}
}

func waitForEndpoint(ctx context.Context, baseURL, key string, httpClient *http.Client) error {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	target := strings.TrimRight(baseURL, "/") + "/models"
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, requestErr := httpClient.Do(req)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("public endpoint returned %s", resp.Status)
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusNotFound {
				return lastErr
			}
		} else {
			lastErr = requestErr
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func status(args []string) error {
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err = fs.Parse(args); err != nil {
		return err
	}
	token := envOr("MODELUPLINK_ACCOUNT_TOKEN", cfg.AccountToken)
	if token == "" {
		return errors.New("not logged in")
	}
	api := client.New(envOr("MODELUPLINK_CONTROL_URL", valueOr(cfg.ControlURL, defaultControlURL)), token)
	items, err := api.ListEndpoints(context.Background())
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	if account, meErr := api.Me(context.Background()); meErr == nil && !entitled(account) && account.TrialStatus == "active" {
		fmt.Printf("Free trial: %d requests and %s transfer left, ends %s\n", account.TrialRemaining, trialTransfer(account.TrialTransferRemaining), trialDate(account.TrialExpiresAt))
	}
	if len(items) == 0 {
		fmt.Println("No endpoints.")
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Slug < items[j].Slug })
	for _, e := range items {
		state := "offline"
		if e.Online {
			state = "online"
		}
		engineColumn := e.Engine
		if e.Trial || cfg.Endpoints[e.Slug].Trial {
			engineColumn = "trial"
		}
		fmt.Printf("%-24s %-7s %-7s %s\n", e.Slug, engineColumn, state, e.URL)
	}
	return nil
}

func logs(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: modeluplink logs ENDPOINT [--follow]")
	}
	slug := args[0]
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow log output")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	location, err := service.LogPath(slug)
	if err != nil {
		return err
	}
	if !*follow {
		fmt.Println(location)
		return nil
	}
	if runtime.GOOS == "linux" {
		cmd := hostexec.Command("journalctl", "--user", "-u", "modeluplink-"+slug+".service", "-f")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if runtime.GOOS == "windows" {
		return followWindowsLog(location)
	}
	cmd := hostexec.Command("tail", "-f", location)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func stop(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: modeluplink stop ENDPOINT")
	}
	if runtime.GOOS == "windows" {
		return stopWindowsEndpoint(args[0])
	}
	if err := service.Stop(args[0]); err != nil {
		return err
	}
	fmt.Println("Stopped", args[0], "(billing remains active until the endpoint is deleted).")
	return nil
}

func deleteEndpoint(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: modeluplink delete ENDPOINT [--yes]")
	}
	slug := args[0]
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	local, ok := cfg.Endpoints[slug]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured locally", slug)
	}
	if !*yes {
		fmt.Printf("Delete %s and revoke all its keys? [y/N] ", slug)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" {
			return errors.New("delete canceled")
		}
	}
	if err = client.New(cfg.ControlURL, cfg.AccountToken).DeleteEndpoint(context.Background(), local.ID); err != nil {
		var apiError *client.APIError
		if !errors.As(err, &apiError) || (apiError.Status != http.StatusUnauthorized && apiError.Status != http.StatusNotFound) {
			return err
		}
		if apiError.Status == http.StatusUnauthorized {
			fmt.Fprintln(os.Stderr, "Account authorization expired; cleaning this machine. Sign in to verify remote endpoint deletion.")
		}
	}
	if err = uninstallService(slug); err != nil {
		return fmt.Errorf("endpoint was revoked, but the local background service could not be removed: %w", err)
	}
	if err = localconfig.DeleteEndpointFiles(local.Slug); err != nil {
		return fmt.Errorf("endpoint was revoked, but local credentials could not be removed: %w", err)
	}
	delete(cfg.Endpoints, slug)
	if err = localconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Deleted", slug)
	return nil
}

func keys(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: modeluplink key create|list|revoke ...")
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	api := client.New(cfg.ControlURL, cfg.AccountToken)
	switch args[0] {
	case "create":
		slug := args[1]
		local, ok := cfg.Endpoints[slug]
		if !ok {
			return fmt.Errorf("unknown local endpoint %q", slug)
		}
		fs := flag.NewFlagSet("key create", flag.ContinueOnError)
		name := fs.String("name", "integration", "key name")
		clientConfig := fs.String("client-config", "", "write a new mode-0600 verified-client credentials file")
		if err = fs.Parse(args[2:]); err != nil {
			return err
		}
		_, secret, err := api.CreateKey(context.Background(), local.ID, *name)
		if err != nil {
			return err
		}
		fmt.Println(secret)
		pinValue, pinErr := endpointPin(local)
		if pinErr == nil {
			fmt.Println("TLS pin:", pinValue)
		}
		fmt.Println("This key is shown once.")
		if *clientConfig != "" {
			if pinErr != nil {
				return fmt.Errorf("create verified-client credentials: %w", pinErr)
			}
			credentials := clientCredentials{Endpoint: local.URL, APIKey: secret, TLSPin: pinValue}
			if err = saveClientCredentials(*clientConfig, credentials); err != nil {
				return fmt.Errorf("write verified-client credentials: %w", err)
			}
			fmt.Println("Verified-client credentials:", *clientConfig)
		}
		return nil
	case "list":
		slug := args[1]
		local, ok := cfg.Endpoints[slug]
		if !ok {
			return fmt.Errorf("unknown local endpoint %q", slug)
		}
		items, err := api.ListKeys(context.Background(), local.ID)
		if err != nil {
			return err
		}
		for _, k := range items {
			state := "active"
			if k.RevokedAt != nil {
				state = "revoked"
			}
			fmt.Printf("%-24s %-20s %s\n", k.ID, k.Name, state)
		}
		return nil
	case "revoke":
		if err = api.RevokeKey(context.Background(), args[1]); err != nil {
			return err
		}
		fmt.Println("Revoked", args[1])
		return nil
	default:
		return errors.New("key command must be create, list, or revoke")
	}
}

func pin(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: modeluplink pin ENDPOINT")
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	endpoint, ok := cfg.Endpoints[args[0]]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured locally", args[0])
	}
	value, err := endpointPin(endpoint)
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func endpointPin(e localconfig.Endpoint) (string, error) {
	endpointURL, err := url.Parse(e.URL)
	if err != nil || endpointURL.Hostname() == "" {
		return "", errors.New("local endpoint configuration has an invalid public URL")
	}
	endpointPath, err := localconfig.EndpointPath(e.Slug)
	if err != nil {
		return "", err
	}
	return agent.CertificatePin(filepath.Join(filepath.Dir(endpointPath), e.Slug+"-certificates"), endpointURL.Hostname())
}

func engineCommand(args []string) error {
	if len(args) != 2 || args[0] != "update" || args[1] != "ollama" {
		return errors.New("usage: modeluplink engine update ollama")
	}
	return engine.UpdateOllama(context.Background())
}

func agentCommand(args []string) error {
	fs := flag.NewFlagSet("_agent", flag.ContinueOnError)
	path := fs.String("config", "", "endpoint configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--config is required")
	}
	endpoint, err := localconfig.LoadEndpoint(*path)
	if err != nil {
		return err
	}
	if endpoint.Stopped {
		return nil
	}
	closeLog, err := service.RedirectAgentLog(endpoint.Slug)
	if err != nil {
		return err
	}
	defer closeLog()
	return runAgent(endpoint)
}

// agentExitGrace bounds agent shutdown when a runtime or upstream request
// does not return after cancellation. The watchdog exits before the service
// manager or Flatpak supervisor reaches its own termination deadline.
const agentExitGrace = 10 * time.Second

func runAgent(e localconfig.Endpoint) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stopWatchdog := context.AfterFunc(ctx, func() {
		time.Sleep(agentExitGrace)
		fmt.Fprintln(os.Stderr, "modeluplink: forcing exit after stop request")
		os.Exit(0)
	})
	defer stopWatchdog()
	return runAgentContext(ctx, e)
}

func runForegroundAgent(e localconfig.Endpoint, apiKey, trialLine string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	agentDone := make(chan error, 1)
	go func() { agentDone <- runAgentContext(ctx, e) }()
	readyCtx, cancelReady := context.WithTimeout(ctx, 5*time.Minute)
	err := waitForReady(readyCtx, e.URL, apiKey, nil)
	cancelReady()
	if err != nil {
		printPendingCredentials(e.URL, apiKey, trialLine)
		cancel()
		<-agentDone
		return fmt.Errorf("endpoint was created but is not ready yet: %w", err)
	}
	printReadyCredentials(e, apiKey, trialLine)
	return <-agentDone
}

func runAgentContext(ctx context.Context, e localconfig.Endpoint) error {
	endpointURL, err := url.Parse(e.URL)
	if err != nil || endpointURL.Hostname() == "" {
		return errors.New("local endpoint configuration has an invalid public URL")
	}
	controlURL := e.ControlURL
	if controlURL == "" {
		controlURL = defaultControlURL
	}
	endpointPath, err := localconfig.EndpointPath(e.Slug)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" && e.ManagesRuntime() && !e.Stopped && !e.Revoked {
		if _, err = engine.EnsureOllama(ctx, false); err != nil {
			return err
		}
	}
	if e.Revoked {
		return retireRevokedEndpoint(ctx, e)
	}
	e.UpstreamKey, err = localconfig.ResolveUpstreamKey(e)
	if err != nil {
		return err
	}
	runner := agent.Runner{Config: agent.Config{EndpointID: e.ID, EndpointHost: endpointURL.Hostname(), AgentToken: e.AgentToken, Engine: e.Engine, RelayURL: e.RelayURL, ControlURL: controlURL, CertificateCache: filepath.Join(filepath.Dir(endpointPath), e.Slug+"-certificates"), UpstreamURL: e.UpstreamURL, UpstreamKey: e.UpstreamKey, AllowLAN: e.AllowLAN, CORSOrigins: e.CORSOrigins, SharedModels: e.SharedModels, ActivityPath: endpointPath + ".activity"}, Logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}
	err = runner.Run(ctx)
	if errors.Is(err, agent.ErrEndpointRevoked) {
		return retireRevokedEndpoint(ctx, e)
	}
	return err
}

func defaultName() string {
	name, err := naming.NewPaidName()
	if err != nil {
		return "home-orbit"
	}
	return name
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
func canonicalOllamaModel(name string) string {
	if !strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		return name + ":latest"
	}
	return name
}
func interactive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}
