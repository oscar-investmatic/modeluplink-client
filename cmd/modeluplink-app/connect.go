package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
)

type localServer struct {
	URL       string   `json:"url"`
	Status    string   `json:"status"`
	Message   string   `json:"message,omitempty"`
	Models    []string `json:"models"`
	Ownership string   `json:"ownership"`
}

func (u *ui) sourceFingerprint() string { return u.localURL + "\n" + u.localKey + "\n" + u.localModel }
func (u *ui) inspectSource(action string) {
	if !u.begin("Checking the local model server…") {
		return
	}
	fields := map[string]string{"action": action, "local_url": u.localURL, "local_key": u.localKey, "model": u.localModel}
	fingerprint := u.sourceFingerprint()
	u.run(func() {
		reply := u.request(fields)
		fyne.DoAndWait(func() {
			if reply != nil {
				if action == "discover_servers" {
					u.servers = reply.Servers
					u.sourceMessage = "Select a detected API address, or enter your own."
					if len(u.servers) == 0 {
						u.sourceMessage = "No local APIs found. Open your runtime app and enable its API server."
					}
				}
				if reply.Source != nil {
					u.localModels = reply.Source.Models
					u.sourceMessage = reply.Source.Message
					if len(u.localModels) > 0 && !containsModel(u.localModels, u.localModel) {
						u.localModel = u.localModels[0]
					}
					if reply.Ready {
						u.testedSource = fingerprint
					} else {
						u.testedSource = ""
					}
				}
			}
			u.finish()
		})
	})
}
func containsModel(models []string, m string) bool {
	for _, v := range models {
		if v == m {
			return true
		}
	}
	return false
}
func (u *ui) attachedSetupScreen() fyne.CanvasObject {
	address := widget.NewEntry()
	address.SetPlaceHolder("http://127.0.0.1:1234/v1")
	address.SetText(u.localURL)
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder("Local server API key (if required)")
	key.SetText(u.localKey)
	picker := widget.NewSelect(u.localModels, nil)
	picker.SetSelected(u.localModel)
	var start *widget.Button
	update := func() {
		if start != nil {
			if (u.trialFinished() || u.testedSource == u.sourceFingerprint() && u.localModel != "") && !u.working() {
				start.Enable()
			} else {
				start.Disable()
			}
		}
	}
	address.OnChanged = func(v string) {
		if v != u.localURL {
			u.localKey = ""
			key.SetText("")
			u.testedSource = ""
		}
		u.localURL = v
		update()
	}
	key.OnChanged = func(v string) { u.localKey = v; update() }
	picker.OnChanged = func(v string) { u.localModel = v; update() }
	detected := []string{}
	for _, s := range u.servers {
		detected = append(detected, s.URL)
	}
	servers := widget.NewSelect(detected, func(v string) { address.SetText(v); u.inspectSource("inspect_source") })
	servers.PlaceHolder = "Detected API servers"
	action := u.connect
	if u.preparingMove {
		action = u.confirmMove
	}
	if u.trialFinished() {
		action = u.billing
	}
	start = widget.NewButton("Start sharing", action)
	if u.trialFinished() {
		start.SetText("Subscribe to Agent")
	}
	start.Importance = widget.HighImportance
	update()
	items := []fyne.CanvasObject{u.title("Connect a model server"), u.secondary("Run models in your favorite app. Share access from this computer.", fyne.TextAlignCenter),
		widget.NewButton("Find local servers", func() { u.inspectSource("discover_servers") }), servers, address, key,
		u.secondary("Enable the API server in your runtime app. Its local key stays on this computer and is separate from the key you share with friends.", fyne.TextAlignLeading),
		widget.NewButton("Find models", func() { u.inspectSource("inspect_source") }), picker,
		u.secondary("Test sends: “Reply with the word ready.” Your runtime may load the model. Chat readiness does not verify tools, vision or local-only execution.", fyne.TextAlignLeading),
		widget.NewButton("Test connection", func() { u.inspectSource("test_source") }), u.secondary(u.sourceMessage, fyne.TextAlignLeading)}
	if u.paid() {
		name := widget.NewEntry()
		name.SetPlaceHolder("Endpoint name")
		name.SetText(u.endpointName)
		name.OnChanged = func(v string) { u.endpointName = v }
		items = append(items, name)
	}
	items = append(items, start)
	if !flatpak.Enabled() {
		items = append(items, widget.NewButton("Help me set up a model with Ollama", func() { u.managedSetup = true; u.render() }))
	}
	items = append(items, u.secondary("Keep the model app and this computer running. Stop sharing disconnects remote access and leaves your app alone.", fyne.TextAlignLeading))
	if u.preparingMove {
		items = append(items, widget.NewButton("Keep using that computer", u.keepOtherComputer))
	}
	if u.working() {
		for _, item := range items {
			switch w := item.(type) {
			case *widget.Button:
				w.Disable()
			case *widget.Entry:
				w.Disable()
			case *widget.Select:
				w.Disable()
			}
		}
	}
	return container.NewVBox(items...)
}
func sourceAvailable(s localServer) bool {
	return s.Status == "server_available" || s.Status == "ready"
}
func (u *ui) modelsFor(e endpoint) []string {
	if s, ok := u.localSources[e.Slug]; ok {
		return s.Models
	}
	return u.models
}
func (u *ui) friendAccess(e endpoint) {
	target, _ := url.Parse(dashboardURL + "#api-keys")
	_ = u.app.OpenURL(target)
}
