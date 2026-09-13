package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const (
	dashboardURL     = "https://modeluplink.com/dashboard/"
	pausedPreference = "pausedConnections"
	defaultModel     = "llama3.2:3b"
	staleAfter       = 30 * time.Second
)

// Intervals are variables so tests can shorten them.
var (
	refreshInterval   = 6 * time.Second
	readinessInterval = 3 * time.Second
	copiedDuration    = 3 * time.Second
)

var suggestedModels = []string{defaultModel, "gemma3:1b", "qwen2.5:7b"}

// connectResult is the one-time view shown after a successful connect: the
// address and the API key that the helper returns exactly once.
type connectResult struct {
	endpoint endpoint
	apiKey   string
	notice   string
}

// ui owns the application state and rebuilds the window content whenever it
// changes. State is only touched on the Fyne thread; helper requests run in
// goroutines and hand results back through fyne.Do.
type ui struct {
	managedSetup                                                bool
	localURL, localKey, localModel, testedSource, sourceMessage string
	localModels                                                 []string
	servers                                                     []localServer
	localSources                                                map[string]localServer
	startupEnabled                                              bool
	app                                                         fyne.App
	window                                                      fyne.Window
	helper                                                      helperRunner
	keys                                                        keyStore
	content                                                     *fyne.Container
	// confirm shows a yes/no question. Tests replace it.
	confirm func(title, message, action string, onYes func())

	email, code, challenge              string
	account                             *account
	unavailable                         bool
	retired                             []endpoint
	connectionLimit                     *endpoint
	otherTrial                          *endpoint
	keepingOtherComputer, preparingMove bool
	endpoints                           []endpoint
	models                              []string
	recommendation                      string
	endpointName                        string
	nameSuggestions                     []string
	selectedModel                       string
	customModel                         string
	progress                            *progressEvent
	progressAt                          time.Time
	connecting, busy                    bool
	loading                             bool
	helperMissing                       bool
	message, errText                    string
	copied                              string
	result                              *connectResult
	paused                              map[string]bool
	memoryKeys                          map[string]string
	sharedModels                        map[string][]string
	activity                            map[string]modelActivity
	memoryReleasePending                map[string]bool
	refreshing, editing                 bool
	revision                            uint64

	// Background work is tracked so tests and shutdown can wait for it.
	pending sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc

	// Rebuilt on every render; kept so progress updates can touch them.
	staleLabel *widget.Label
}

func newUI(app fyne.App, window fyne.Window, helper helperRunner, keys keyStore) *ui {
	ctx, cancel := context.WithCancel(context.Background())
	u := &ui{
		app:           app,
		localURL:      "http://127.0.0.1:1234/v1",
		localSources:  map[string]localServer{},
		window:        window,
		helper:        helper,
		keys:          keys,
		content:       container.NewStack(),
		loading:       true,
		selectedModel: defaultModel,
		paused:        map[string]bool{},
		memoryKeys:    map[string]string{},
		sharedModels:  map[string][]string{}, activity: map[string]modelActivity{}, memoryReleasePending: map[string]bool{},
		ctx:    ctx,
		cancel: cancel,
	}
	for _, slug := range app.Preferences().StringList(pausedPreference) {
		u.paused[slug] = true
	}
	u.confirm = func(title, message, action string, onYes func()) {
		d := dialog.NewConfirm(title, message, func(yes bool) {
			if yes {
				onYes()
			}
		}, u.window)
		d.SetConfirmText(action)
		d.Show()
	}
	u.render()
	return u
}

// stop cancels background work. Callers wait on pending afterwards.
func (u *ui) stop() { u.cancel() }

func (u *ui) working() bool { return u.busy || u.loading }

func (u *ui) connected() bool {
	return !u.trialFinished() && slices.ContainsFunc(u.endpoints, func(e endpoint) bool {
		source, known := u.localSources[e.Slug]
		return e.Online && !u.paused[e.Slug] && (!known || sourceAvailable(source))
	})
}

func (u *ui) paid() bool {
	return u.account != nil && (u.account.BillingState == "active" || u.account.BillingState == "grace")
}

func (u *ui) trialFinished() bool {
	return !u.paid() && u.account != nil && (u.account.TrialStatus == "expired" || u.account.TrialStatus == "exhausted")
}

func (u *ui) allowance() string {
	if u.account != nil && (u.account.BillingState == "active" || u.account.BillingState == "grace") {
		return "Your subscription is ready"
	}
	if u.trialFinished() {
		return "Subscribe to keep your model connected"
	}
	if u.account != nil && u.account.TrialStatus == "active" {
		text := "Free trial: " + strconv.Itoa(u.account.TrialRemaining) + " requests left"
		if u.account.TrialTransferRemaining != nil {
			text += " · " + formatBytes(*u.account.TrialTransferRemaining) + " transfer left"
		}
		return text
	}
	return "500 requests · 250 MB · 7 days · no card needed"
}

// run executes fn on a goroutine tracked by pending.
func (u *ui) run(fn func()) {
	u.pending.Go(fn)
}

// request performs one helper call from a background goroutine. The helper's
// friendly `error` field, or a friendly transport error, lands in errText and
// nil is returned; a reply is returned only when the action succeeded.
func (u *ui) request(fields map[string]string) *helperReply {
	reply, err := u.helper.run(u.ctx, fields, func(p progressEvent) {
		fyne.DoAndWait(func() { p.Message = computerCopy(p.Message); u.receive(p) })
	})
	if err != nil {
		text := "Something interrupted the connection. Please try again."
		switch {
		case errors.Is(err, errHelperMissing):
			fyne.DoAndWait(func() { u.helperMissing = true })
			text = err.Error()
		case errors.Is(err, errHelper), errors.Is(err, errHelperTimeout):
			text = err.Error()
		}
		fyne.DoAndWait(func() { u.errText = text })
		return nil
	}
	if reply.Error != "" {
		fyne.DoAndWait(func() { u.errText = computerCopy(reply.Error) })
		return nil
	}
	return &reply
}

func (u *ui) receive(p progressEvent) {
	u.progress = &p
	u.progressAt = time.Now()
	u.message = p.Message
	u.render()
}

func (u *ui) apply(reply *helperReply) {
	if reply.StartupEnabled != nil {
		u.startupEnabled = *reply.StartupEnabled
	}
	u.applySharing(reply)
	u.activity = reply.Activity // A full state with no activity must clear stale counters.
	u.unavailable = reply.Unavailable
	u.account = reply.Account
	u.endpoints = reply.Endpoints
	if u.result != nil && !reply.Unavailable && !slices.ContainsFunc(u.endpoints, func(e endpoint) bool { return e.ID == u.result.endpoint.ID }) {
		u.result = nil
	}
	if endpointID(u.otherTrial) != endpointID(reply.OtherTrial) {
		u.keepingOtherComputer, u.preparingMove = false, false
	}
	u.otherTrial = reply.OtherTrial
	u.connectionLimit = reply.ConnectionLimit
	u.retired = reply.Retired
	u.models = reply.Models
	u.nameSuggestions = reply.NameSuggestions
	if u.endpointName == "" && len(u.nameSuggestions) > 0 {
		u.endpointName = u.nameSuggestions[0]
	}
	sort.Strings(u.models)
	if reply.Recommendation != "" {
		u.recommendation = reply.Recommendation
	}
	if len(u.models) > 0 && !slices.Contains(u.models, u.selectedModel) {
		u.selectedModel = u.models[0]
	}
}

// stateSignature summarises every field a quiet refresh can change, so the
// screen is rebuilt only when the helper reports something new.
func (u *ui) stateSignature() string {
	snapshot := struct {
		Account         *account
		Sources         any
		Endpoints       any
		Retired         any
		ConnectionLimit *endpoint
		OtherTrial      *endpoint
		Models          any
		Recommendation  string
		NameSuggestions any
		Shared          any
		Activity        any
		Stopped         any
		Release         any
		Unavailable     bool
		HelperMissing   bool
		Err             string
	}{u.account, nilIfEmpty(u.localSources), nilIfEmpty(u.endpoints), nilIfEmpty(u.retired), u.connectionLimit, u.otherTrial, nilIfEmpty(u.models), u.recommendation, nilIfEmpty(u.nameSuggestions), nilIfEmpty(u.sharedModels), nilIfEmpty(u.activity), nilIfEmpty(u.paused), nilIfEmpty(u.memoryReleasePending), u.unavailable, u.helperMissing, u.errText}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("%+v", snapshot)
	}
	return string(encoded)
}

// nilIfEmpty makes an empty and an absent collection compare equal in a
// signature; the helper omits maps it has nothing to say about.
func nilIfEmpty[T any](v T) any {
	if reflect.ValueOf(v).Len() == 0 {
		return nil
	}
	return v
}

// begin marks the start of a helper action from the Fyne thread and returns
// false when another action is already running.
func (u *ui) begin(message string) bool {
	if u.working() {
		return false
	}
	u.revision++
	u.busy, u.errText, u.message = true, "", message
	u.render()
	return true
}

func (u *ui) finish() {
	u.busy, u.message = false, ""
	u.render()
}

// refresh loads account, connections, and installed models. Quiet refreshes
// keep the current screen and errors while polling in the background.
func (u *ui) refresh(quiet bool) {
	if u.busy || u.refreshing || u.editing {
		return
	}
	u.refreshing = true
	revision := u.revision
	if !quiet {
		u.loading, u.errText = true, ""
		u.render()
	}
	u.run(func() {
		reply, err := u.helper.run(u.ctx, map[string]string{"action": "state"}, nil)
		fyne.DoAndWait(func() {
			u.refreshing = false
			if revision != u.revision || u.busy {
				return
			}
			before := u.stateSignature()
			if err == nil && reply.Error == "" {
				u.helperMissing = false
				u.apply(&reply)
			} else if !quiet {
				u.helperMissing = errors.Is(err, errHelperMissing)
				if reply.Error != "" {
					u.errText = computerCopy(reply.Error)
				} else {
					u.errText = "We couldn’t refresh your connections. Try again."
				}
			}
			u.loading = false
			// A quiet refresh redraws only when something visible changed.
			// Rebuilding the window on every poll flickers and replaces
			// focused controls; most polls return an identical state.
			if !quiet || before != u.stateSignature() {
				u.render()
			}
		})
	})
}

func (u *ui) sendCode() {
	email := strings.TrimSpace(u.email)
	if !u.begin("Sending your code…") {
		return
	}
	u.run(func() {
		reply := u.request(map[string]string{"action": "start_code", "email": email})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.challenge, u.code = reply.Challenge, ""
			}
			u.finish()
		})
	})
}

func (u *ui) verify() {
	challenge, code := u.challenge, u.code
	if challenge == "" || len(code) != 6 || !u.begin("Checking your code…") {
		return
	}
	u.run(func() {
		reply := u.request(map[string]string{"action": "verify_code", "challenge_id": challenge, "code": code})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.challenge, u.code = "", ""
				u.apply(reply)
				if reply.Notice != "" {
					u.errText = reply.Notice
				}
			}
			u.finish()
		})
	})
}

func (u *ui) chosenModel() string {
	if custom := strings.TrimSpace(u.customModel); custom != "" {
		return custom
	}
	return u.selectedModel
}

func (u *ui) connect() { u.connectWithMove("") }

func (u *ui) connectWithMove(moveID string) {
	model := u.chosenModel()
	localURL, localKey := "", ""
	if !u.managedSetup && u.localModel != "" {
		model = u.localModel
		localURL = u.localURL
		localKey = u.localKey
	}
	if !u.begin("") {
		return
	}
	u.connecting, u.result = true, nil
	u.receive(progressEvent{Event: "progress", Stage: "account", Message: "Checking your account…"})
	done := make(chan struct{})
	u.run(func() { u.watchStale(done) })
	u.run(func() {
		defer close(done)
		fields := map[string]string{"action": "connect", "model": model}
		if localURL != "" {
			fields["local_url"] = localURL
			fields["local_key"] = localKey
		}
		if u.account != nil && (u.account.BillingState == "active" || u.account.BillingState == "grace") {
			fields["endpoint_name"] = u.endpointName
		}
		if moveID != "" {
			fields["action"], fields["move_trial_id"] = "move_trial", moveID
		}
		reply := u.request(fields)
		fyne.DoAndWait(func() {
			u.connecting = false
			if reply != nil && reply.Endpoint != nil {
				u.localKey = ""
				u.testedSource = ""
			}
			if reply != nil && reply.Endpoint != nil {
				u.applySharing(reply)
				u.endpoints = append(u.endpoints, *reply.Endpoint)
				u.otherTrial, u.preparingMove, u.keepingOtherComputer = nil, false, false
				u.connectionLimit = nil
				u.result = &connectResult{endpoint: *reply.Endpoint, apiKey: reply.APIKey, notice: reply.Notice}
				if reply.APIKey != "" {
					u.saveKey(reply.Endpoint.Slug, reply.APIKey)
				}
			} else if reply != nil && reply.Notice != "" {
				u.errText = reply.Notice
			}
			u.finish()
		})
		if reply != nil && reply.Endpoint != nil && !reply.Endpoint.Online {
			u.awaitOnline(reply.Endpoint.Slug)
		}
	})
}

// watchStale reports how long a quiet setup step has been silent.
func (u *ui) watchStale(done <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			fyne.Do(u.updateStale)
		}
	}
}

func (u *ui) updateStale() {
	if u.staleLabel == nil || !u.connecting {
		return
	}
	quiet := time.Since(u.progressAt)
	if quiet < staleAfter {
		u.staleLabel.SetText("")
		return
	}
	u.staleLabel.SetText("No new update for " + strconv.Itoa(int(quiet.Seconds())) + " seconds. Waiting for this step to respond…")
}

// awaitOnline polls state after a reserved-but-connecting result until the
// new endpoint reports online or the user leaves the result screen.
func (u *ui) awaitOnline(slug string) {
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-time.After(readinessInterval):
		}
		var keepGoing bool
		fyne.DoAndWait(func() { keepGoing = u.result != nil && u.result.endpoint.Slug == slug && !u.result.endpoint.Online })
		if !keepGoing {
			return
		}
		reply, err := u.helper.run(u.ctx, map[string]string{"action": "state"}, nil)
		if err != nil || reply.Error != "" {
			continue
		}
		var online bool
		fyne.DoAndWait(func() {
			if u.result == nil || u.result.endpoint.Slug != slug {
				online = true
				return
			}
			u.apply(&reply)
			for _, e := range reply.Endpoints {
				if e.Slug == slug {
					u.result.endpoint = e
					online = e.Online
				}
			}
			if online {
				u.result.notice = ""
			}
			u.render()
		})
		if online {
			return
		}
	}
}

// act sends pause, resume, delete, or new_key for one connection.
func (u *ui) act(action string, target endpoint) {
	message := "Updating your connection…"
	if action == "resume" {
		message = "Reconnecting…"
	}
	if !u.begin(message) {
		return
	}
	u.run(func() {
		reply := u.request(map[string]string{"action": action, "slug": target.Slug})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.applySharing(reply)
				if reply.Notice != "" {
					u.errText = computerCopy(reply.Notice)
				}
				switch action {
				case "pause":
					u.paused[target.Slug] = true
					if pending, ok := reply.MemoryReleasePending[target.Slug]; ok && !pending {
						u.retired = slices.DeleteFunc(u.retired, func(e endpoint) bool { return e.ID == target.ID })
					}
				case "resume":
					delete(u.paused, target.Slug)
					delete(u.memoryReleasePending, target.Slug)
				case "delete":
					delete(u.paused, target.Slug)
					u.keys.remove(target.Slug)
					delete(u.memoryKeys, target.Slug)
					u.endpoints = slices.DeleteFunc(u.endpoints, func(e endpoint) bool { return e.ID == target.ID })
				}
				u.savePaused()
				if reply.APIKey != "" {
					u.saveKey(target.Slug, reply.APIKey)
					u.copy(reply.APIKey, "API key")
				}
			}
			u.finish()
		})
	})
}

func (u *ui) savePaused() {
	slugs := make([]string, 0, len(u.paused))
	for slug := range u.paused {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	u.app.Preferences().SetStringList(pausedPreference, slugs)
}

// copyKey copies a stored key, or creates one when this app has none.
func (u *ui) copyKey(target endpoint) {
	if key, ok := u.memoryKeys[target.Slug]; ok {
		u.copy(key, "API key")
		return
	}
	key, err := u.keys.read(target.Slug)
	if err == nil {
		u.memoryKeys[target.Slug] = key
		u.copy(key, "API key")
		return
	}
	// A cancelled unlock is not evidence that the key is missing. Creating a
	// replacement here would leave paid accounts with an unexpected extra key.
	if !errors.Is(err, errKeyNotFound) {
		u.errText = errKeyringRead.Error()
		u.render()
		return
	}
	u.act("new_key", target)
}

func (u *ui) saveKey(slug, key string) {
	u.memoryKeys[slug] = key
	if err := u.keys.save(slug, key); err != nil {
		u.errText = err.Error()
	}
}

func (u *ui) copy(text, label string) {
	u.app.Clipboard().SetContent(text)
	status := label + " copied"
	u.copied = status
	u.render()
	u.run(func() {
		select {
		case <-u.ctx.Done():
			return
		case <-time.After(copiedDuration):
		}
		fyne.Do(func() {
			if u.copied == status {
				u.copied = ""
				u.render()
			}
		})
	})
}

func (u *ui) signOut() {
	if !u.begin("Signing out…") {
		return
	}
	u.run(func() {
		reply := u.request(map[string]string{"action": "sign_out"})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.account, u.endpoints, u.result = nil, nil, nil
				u.localKey, u.testedSource = "", ""
				u.otherTrial, u.preparingMove, u.keepingOtherComputer = nil, false, false
				u.connectionLimit = nil
				u.retired = nil
				u.memoryKeys = map[string]string{}
				u.challenge, u.code, u.unavailable = "", "", false
			}
			u.finish()
		})
	})
}

func (u *ui) dashboard() {
	if link, err := url.Parse(dashboardURL); err == nil {
		_ = u.app.OpenURL(link)
	}
}

func (u *ui) billing() {
	destination := dashboardURL + "?plan=agent#billing-panel"
	if u.paid() {
		destination = dashboardURL + "#billing-panel"
	}
	if link, err := url.Parse(destination); err == nil {
		_ = u.app.OpenURL(link)
	}
}

// startBackgroundRefresh keeps connection status current while signed in.
func (u *ui) startBackgroundRefresh() {
	u.run(func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-ticker.C:
				fyne.Do(func() {
					if u.account != nil && !u.connecting && u.result == nil {
						u.refresh(true)
					}
				})
			}
		}
	})
}

// Action responses are partial: never clear account state when applying them.
func (u *ui) applySharing(reply *helperReply) {
	if reply.LocalSources != nil {
		if u.localSources == nil {
			u.localSources = map[string]localServer{}
		}
		for k, v := range reply.LocalSources {
			u.localSources[k] = v
		}
	}
	if reply.SharedModels != nil {
		for slug, models := range reply.SharedModels {
			u.sharedModels[slug] = models
		}
	}
	if reply.Activity != nil {
		u.activity = reply.Activity
	}
	for slug, stopped := range reply.Stopped {
		u.paused[slug] = stopped
	}
	for slug, pending := range reply.MemoryReleasePending {
		u.memoryReleasePending[slug] = pending
	}
}

func computerCopy(text string) string {
	return strings.NewReplacer("this Mac", "this computer", "your Mac", "your computer", "Keep this Mac", "Keep this computer", "System Settings → General → Login Items", "your desktop’s background-service settings").Replace(text)
}

func (u *ui) updateSharing(target endpoint, selected []string) {
	if len(selected) == 0 || len(selected) > 8 || !u.begin("Checking shared models…") {
		return
	}
	u.run(func() {
		modelIDs, _ := json.Marshal(selected)
		reply := u.request(map[string]string{"action": "share_models", "slug": target.Slug, "model_ids": string(modelIDs)})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.applySharing(reply)
				u.paused[target.Slug] = false
				u.memoryReleasePending[target.Slug] = false
				u.savePaused()
			}
			u.finish()
		})
	})
}
