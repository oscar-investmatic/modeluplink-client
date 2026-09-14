package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Copy shared with the Mac app, with "this computer" in place of "this Mac".
const (
	tagline          = "Your model. This computer. Anywhere."
	keepAwakeSetup   = "Keep this computer awake and the app open while we finish setup."
	keepAwakeRunning = "Keep this computer awake and online. Your connection keeps running when you close the app. Stopping sharing doesn’t cancel a subscription."
	removeQuestion   = "Remove this connection?"
	removeDetail     = "Its address and API keys will stop working. Your downloaded models stay on this computer."
	signOutQuestion  = "Sign out?"
	signOutDetail    = "Your connections keep running. Sign in again any time."
	keyShownOnce     = "This key is shown once. Copy it now; it’s also saved in your system keyring."
)

var (
	setupStages = []string{"account", "prepare", "download", "modelcheck", "reserve", "start", "verify"}
	setupTitles = []string{"Check account", "Prepare this computer", "Get model ready", "Check model response", "Reserve address", "Start connection", "Check public connection"}
)

// render rebuilds the whole window content from state. Screens are small, so
// rebuilding is simpler and safer than keeping many widgets in sync.
func (u *ui) render() {
	u.staleLabel = nil
	var body fyne.CanvasObject
	switch {
	case u.helperMissing:
		body = u.helperMissingScreen()
	case u.loading && u.account == nil:
		body = container.NewVBox(u.orbit(), u.secondary("Waking up your uplink…", fyne.TextAlignCenter))
	case u.account == nil:
		body = u.signInScreen()
	case u.unavailable:
		body = container.NewVBox(u.orbit(), u.title("You’re signed in."), u.primary("Refresh connections", false, func() { u.refresh(false) }))
	case u.connecting:
		body = u.connectingScreen()
	case u.result != nil:
		body = u.resultScreen()
	case u.otherTrial != nil && !u.preparingMove:
		body = u.otherComputerScreen()
	case len(u.endpoints) == 0 && u.connectionLimit != nil:
		body = u.connectionLimitScreen()
	case len(u.endpoints) == 0:
		body = u.setupScreen()
	default:
		body = u.connectionsScreen()
	}
	items := []fyne.CanvasObject{body}
	if u.account != nil && !u.unavailable && !u.connecting {
		if len(u.endpoints) == 0 && !u.managedSetup && u.otherTrial == nil {
			action := "Subscribe to Agent"
			if u.paid() {
				action = "Manage billing"
			}
			bill := widget.NewButton(action, u.billing)
			bill.Importance = widget.LowImportance
			items = append(items, u.secondary(u.allowance(), fyne.TextAlignCenter), bill)
		} else {
			items = append([]fyne.CanvasObject{u.billingCard()}, items...)
		}
	}
	for _, e := range u.retired {
		items = append(items, u.secondary("Sharing stopped on this computer, but model memory release is not confirmed.", fyne.TextAlignCenter), u.primary("Release model memory", false, func() { u.act("pause", e) }))
	}
	if u.errText != "" {
		problem := widget.NewLabelWithStyle(u.errText, fyne.TextAlignCenter, fyne.TextStyle{})
		problem.Wrapping = fyne.TextWrapWord
		problem.Importance = widget.WarningImportance
		items = append([]fyne.CanvasObject{problem}, items...)
	}
	if u.updateInstalled {
		// Clicking the app icon only refocuses this window, so the update
		// prompt stays hidden until the user closes it.
		notice := widget.NewLabelWithStyle(updateInstalledNotice, fyne.TextAlignCenter, fyne.TextStyle{})
		notice.Wrapping = fyne.TextWrapWord
		notice.Importance = widget.WarningImportance
		items = append([]fyne.CanvasObject{notice}, items...)
	}
	if u.working() && !u.connecting {
		items = append(items, container.NewCenter(widget.NewActivity()))
		if u.message != "" {
			items = append(items, u.secondary(u.message, fyne.TextAlignCenter))
		}
	}
	scroll := container.NewVScroll(container.NewPadded(container.NewVBox(items...)))
	u.content.Objects = []fyne.CanvasObject{container.NewBorder(u.header(), u.footer(), nil, nil, scroll)}
	u.content.Refresh()
}

func (u *ui) header() fyne.CanvasObject {
	mark := canvas.NewText("MU", theme.Color(theme.ColorNamePrimary))
	mark.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	mark.TextSize = 16
	name := widget.NewLabelWithStyle("Model Uplink", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	items := []fyne.CanvasObject{container.NewCenter(mark), name, layout.NewSpacer()}
	if u.account != nil {
		email := u.secondary(u.account.Email, fyne.TextAlignTrailing)
		signOut := widget.NewButton("Sign out", func() {
			u.confirm(signOutQuestion, signOutDetail, "Sign out", u.signOut)
		})
		signOut.Importance = widget.LowImportance
		if u.working() {
			signOut.Disable()
		}
		items = append(items, email, signOut)
	}
	items = append(items, desktopSettings(u)...)
	return container.NewHBox(items...)
}

func (u *ui) footer() fyne.CanvasObject {
	dot := canvas.NewCircle(theme.Color(theme.ColorNameDisabled))
	if u.connected() {
		dot.FillColor = theme.Color(theme.ColorNamePrimary)
	}
	dot.Resize(fyne.NewSize(6, 6))
	status := tagline
	if u.copied != "" {
		status = u.copied
	}
	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { u.refresh(false) })
	refresh.Importance = widget.LowImportance
	if u.working() {
		refresh.Disable()
	}
	return container.NewHBox(container.NewCenter(container.NewGridWrap(fyne.NewSize(6, 6), dot)), u.secondary(status, fyne.TextAlignLeading), layout.NewSpacer(), refresh)
}

func (u *ui) helperMissingScreen() fyne.CanvasObject {
	explain := u.secondary("Part of the app is missing. Reinstall Model Uplink, then try again.", fyne.TextAlignCenter)
	return container.NewVBox(u.orbit(), u.title("Let’s repair the app."), explain, u.primary("Try again", false, func() {
		u.helperMissing, u.errText = false, ""
		u.refresh(false)
	}))
}

func (u *ui) signInScreen() fyne.CanvasObject {
	if u.challenge == "" {
		email := widget.NewEntry()
		email.SetPlaceHolder("Email address")
		email.SetText(u.email)
		email.OnSubmitted = func(string) { u.sendCode() }
		if u.working() {
			email.Disable()
		}
		send := u.primary("Send sign-in code", !strings.Contains(u.email, "@"), u.sendCode)
		email.OnChanged = func(text string) {
			u.email = text
			if strings.Contains(text, "@") && !u.working() {
				send.Enable()
			} else {
				send.Disable()
			}
		}
		return container.NewVBox(u.orbit(), u.title("Let’s give your model wings."),
			u.secondary("Connect this computer. Use your models anywhere.", fyne.TextAlignCenter), email, send)
	}
	code := widget.NewEntry()
	code.SetPlaceHolder("000000")
	code.TextStyle = fyne.TextStyle{Monospace: true}
	code.SetText(u.code)
	signIn := u.primary("Sign in", len(u.code) != 6, u.verify)
	code.OnChanged = func(text string) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, text)
		if len(digits) > 6 {
			digits = digits[:6]
		}
		if digits != text {
			code.SetText(digits)
			return
		}
		u.code = digits
		if len(digits) == 6 && !u.working() {
			signIn.Enable()
			u.verify()
		} else {
			signIn.Disable()
		}
	}
	code.OnSubmitted = func(string) {
		if len(u.code) == 6 {
			u.verify()
		}
	}
	if u.working() {
		code.Disable()
	}
	newCode := widget.NewButton("Send a new code", u.sendCode)
	newCode.Importance = widget.LowImportance
	different := widget.NewButton("Different email", func() {
		u.challenge, u.errText, u.code = "", "", ""
		u.render()
	})
	different.Importance = widget.LowImportance
	if u.working() {
		newCode.Disable()
		different.Disable()
	}
	return container.NewVBox(u.orbit(), u.title("You’ve got a code."),
		u.secondary("Enter the six digits we emailed to "+u.email+".\nThe code expires in 10 minutes.", fyne.TextAlignCenter),
		code, signIn, container.NewHBox(newCode, layout.NewSpacer(), different))
}

func (u *ui) setupScreen() fyne.CanvasObject {
	if !u.managedSetup {
		return u.attachedSetupScreen()
	}
	var nameCard fyne.CanvasObject = container.NewVBox()
	if u.account != nil && (u.account.BillingState == "active" || u.account.BillingState == "grace") {
		name := widget.NewEntry()
		name.SetPlaceHolder("atlas or home-rover")
		name.SetText(u.endpointName)
		name.OnChanged = func(text string) { u.endpointName = text }
		suggestions := widget.NewSelect(u.nameSuggestions, func(selected string) {
			if selected != "" {
				name.SetText(selected)
			}
		})
		suggestions.PlaceHolder = "Try a suggested name"
		if u.working() {
			name.Disable()
			suggestions.Disable()
		}
		nameLabel := widget.NewLabelWithStyle("ENDPOINT NAME", fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
		nameHint := u.secondary("Choose your own or use a suggestion. We’ll pair it with an available planet, such as atlas.mlup-mars.com.", fyne.TextAlignLeading)
		nameCard = widget.NewCard("", "", container.NewVBox(nameLabel, name, suggestions, nameHint))
	}
	var options, names []string
	for _, name := range u.models {
		options = append(options, name+" · on this computer")
		names = append(names, name)
	}
	suggestions := append([]string{}, suggestedModels...)
	if u.recommendation != "" && !slices.Contains(suggestions, u.recommendation) {
		suggestions = append([]string{u.recommendation}, suggestions...)
	}
	for _, name := range suggestions {
		if !slices.Contains(u.models, name) {
			options = append(options, name+" · download")
			names = append(names, name)
		}
	}
	picker := widget.NewSelect(options, nil)
	if index := slices.Index(names, u.selectedModel); index >= 0 {
		picker.SetSelectedIndex(index)
	} else if len(names) > 0 {
		u.selectedModel = names[0]
		picker.SetSelectedIndex(0)
	}
	hint := u.secondary("", fyne.TextAlignLeading)
	updateHint := func() {
		if slices.Contains(u.models, u.chosenModel()) {
			hint.SetText("Only this model will be shared. It’s already installed; no download needed.")
		} else {
			hint.SetText("Ollama is set up automatically. This model needs a download that may be several GB.")
		}
	}
	picker.OnChanged = func(string) {
		if index := picker.SelectedIndex(); index >= 0 && index < len(names) {
			u.selectedModel = names[index]
		}
		updateHint()
	}
	custom := widget.NewEntry()
	custom.SetPlaceHolder("Or type any Ollama model name")
	custom.SetText(u.customModel)
	custom.OnChanged = func(text string) {
		u.customModel = text
		updateHint()
	}
	updateHint()
	if u.working() {
		picker.Disable()
		custom.Disable()
	}
	label := widget.NewLabelWithStyle("MODEL TO SHARE", fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
	card := widget.NewCard("", "", container.NewVBox(label, picker, custom, hint))
	action := u.primary("Connect my model", false, u.connect)
	if u.preparingMove {
		action = u.primary("Move sharing to this computer", false, u.confirmMove)
	}
	if u.trialFinished() {
		action = u.primary("Subscribe to Agent", false, u.billing)
	}
	var cancel fyne.CanvasObject = container.NewVBox()
	if u.preparingMove {
		cancel = u.primary("Keep using that computer", false, u.keepOtherComputer)
	}
	return container.NewVBox(u.orbit(), u.title("Small app. Big uplink."),
		u.secondary("Pick a model. We’ll take care of the rest.", fyne.TextAlignCenter), nameCard, card, action, cancel,
		u.secondary(u.allowance(), fyne.TextAlignCenter), widget.NewButton("Connect an existing server", func() { u.managedSetup = false; u.render() }))
}

func (u *ui) connectingScreen() fyne.CanvasObject {
	model := widget.NewLabelWithStyle(u.chosenModel(), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	items := []fyne.CanvasObject{u.title("Giving your model wings."), model}
	if p := u.progress; p != nil {
		current := slices.Index(setupStages, p.Stage)
		for index, name := range setupTitles {
			marker := "○"
			style := fyne.TextStyle{}
			switch {
			case index < current:
				marker = "✓"
			case index == current:
				marker = "●"
				style.Bold = true
			}
			items = append(items, widget.NewLabelWithStyle(marker+"  "+name, fyne.TextAlignLeading, style))
		}
		message := widget.NewLabel(p.Message)
		message.Wrapping = fyne.TextWrapWord
		items = append(items, message)
		if fraction := p.fraction(); fraction >= 0 {
			bar := widget.NewProgressBar()
			bar.SetValue(fraction)
			bytes := u.secondary(fmt.Sprintf("%s of %s", formatBytes(p.Completed), formatBytes(p.Total)), fyne.TextAlignLeading)
			percent := u.secondary(strconv.Itoa(int(fraction*100))+"%", fyne.TextAlignTrailing)
			items = append(items, bar, container.NewHBox(bytes, layout.NewSpacer(), percent),
				u.secondary("Progress for the current model file.", fyne.TextAlignLeading))
		} else {
			items = append(items, widget.NewProgressBarInfinite())
		}
		u.staleLabel = u.secondary("", fyne.TextAlignLeading)
		items = append(items, u.staleLabel)
		u.updateStale()
	}
	items = append(items, u.secondary(keepAwakeSetup, fyne.TextAlignLeading))
	return widget.NewCard("", "", container.NewVBox(items...))
}

func (u *ui) resultScreen() fyne.CanvasObject {
	r := u.result
	heading, detail := "Hello, world.", "Your model is ready to go places."
	if r.notice != "" {
		heading, detail = "Almost there.", r.notice
	} else if !r.endpoint.Online {
		heading, detail = "Almost there.", "Your address is reserved. We’re checking the public connection…"
	}
	address := widget.NewLabelWithStyle(r.endpoint.URL, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	address.Wrapping = fyne.TextWrapBreak
	items := []fyne.CanvasObject{u.caption("ADDRESS"), address, widget.NewButton("Copy address", func() { u.copy(r.endpoint.URL, "Address") })}
	if r.apiKey != "" {
		key := widget.NewLabelWithStyle(r.apiKey, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		key.Wrapping = fyne.TextWrapBreak
		items = append(items, u.caption("API KEY"), key, widget.NewButton("Copy API key", func() { u.copy(r.apiKey, "API key") }),
			u.secondary(keyShownOnce, fyne.TextAlignLeading))
	}
	done := u.primary("Done", false, func() {
		u.result = nil
		u.refresh(false)
	})
	return container.NewVBox(u.title(heading), u.secondary(detail, fyne.TextAlignCenter), widget.NewCard("", "", container.NewVBox(items...)), done)
}

func (u *ui) connectionsScreen() fyne.CanvasObject {
	heading, detail := "Your uplink is here.", "Connect when you’re ready."
	if u.connected() {
		heading, detail = "Hello, world.", "Your model is ready to go places."
	} else if u.trialFinished() {
		detail = "Your address and API keys are saved for when you subscribe."
	}
	items := []fyne.CanvasObject{u.orbit(), u.title(heading), u.secondary(detail, fyne.TextAlignCenter)}
	for _, e := range u.endpoints {
		items = append(items, u.endpointCard(e))
	}
	dashboard := widget.NewButton("Open dashboard", u.dashboard)
	dashboard.Importance = widget.LowImportance
	items = append(items, u.secondary(u.allowance(), fyne.TextAlignCenter), u.secondary(keepAwakeRunning, fyne.TextAlignCenter), dashboard)
	return container.NewVBox(items...)
}

func (u *ui) endpointCard(e endpoint) fyne.CanvasObject {
	status := "CONNECTING"
	if u.paused[e.Slug] {
		status = "STOPPED"
	} else if u.trialFinished() {
		status = "TRIAL FINISHED"
	} else if e.Online {
		status = "ONLINE"
	}
	if source, ok := u.localSources[e.Slug]; ok && e.Online && !u.paused[e.Slug] && !u.trialFinished() && !sourceAvailable(source) {
		status = "LOCAL SERVER NEEDS ATTENTION"
	}
	title := u.caption(status)
	remove := widget.NewButton("Remove…", func() { u.confirm(removeQuestion, removeDetail, "Remove", func() { u.act("delete", e) }) })
	remove.Importance = widget.LowImportance
	items := []fyne.CanvasObject{container.NewHBox(title, layout.NewSpacer(), remove)}
	if e.Online && !u.paused[e.Slug] && !u.trialFinished() {
		items = append(items, u.secondary("You can close this window. Your model stays online.", fyne.TextAlignLeading))
	}
	models := "All installed models are shared"
	if selected := u.sharedModels[e.Slug]; selected != nil {
		models = strings.Join(selected, ", ")
	}
	items = append(items, u.secondary(models, fyne.TextAlignLeading))
	if activity, ok := u.activity[e.Slug]; ok && e.Online && !u.paused[e.Slug] && !u.trialFinished() {
		text := fmt.Sprintf("Ready for requests · %d waiting", activity.Waiting)
		if activity.Running > 0 {
			text = fmt.Sprintf("Serving %s · %d running · %d waiting", activity.Model, activity.Running, activity.Waiting)
		}
		items = append(items, u.secondary(text, fyne.TextAlignLeading))
	}
	choose := widget.NewButton("Choose shared models…", func() { u.editSharing(e) })
	address := widget.NewLabelWithStyle(e.URL, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	address.Wrapping = fyne.TextWrapBreak
	copyAddress := widget.NewButton("Copy address", func() { u.copy(e.URL, "Address") })
	copyKey := widget.NewButton("Copy API key", func() { u.copyKey(e) })
	items = append(items, choose, address, container.New(&spacedRow{gap: 16}, copyAddress, copyKey))
	if source, ok := u.localSources[e.Slug]; ok {
		items = append(items, u.secondary(source.URL+" · "+source.Message, fyne.TextAlignLeading))
	}
	items = append(items, widget.NewButton("Manage friends’ access", func() { u.friendAccess(e) }), u.secondary("Create a separate named key for each friend in the dashboard. The GPU owner can access requests processed by their runtime.", fyne.TextAlignLeading))
	if u.paused[e.Slug] {
		text := "Sharing stopped · model memory released"
		external := u.localSources[e.Slug].Ownership == "external"
		if external {
			text = "Sharing stopped · your model app is unchanged"
		}
		pending, known := u.memoryReleasePending[e.Slug]
		if (!known || pending) && !external {
			text = "Sharing stopped · memory release needs attention"
		}
		items = append(items, u.secondary(text, fyne.TextAlignLeading))
		if (!known || pending) && !external {
			retry := widget.NewButton("Release model memory", func() { u.act("pause", e) })
			if u.working() {
				retry.Disable()
			}
			items = append(items, retry)
		}
	}
	label, action := "Stop sharing", "pause"
	if u.paused[e.Slug] {
		label, action = "Start sharing", "resume"
	}
	toggle := widget.NewButton(label, func() { u.act(action, e) })
	if u.paused[e.Slug] && u.trialFinished() {
		toggle = widget.NewButton("Subscribe to resume sharing", u.billing)
	}
	if u.working() {
		for _, button := range []*widget.Button{choose, copyAddress, copyKey, toggle, remove} {
			button.Disable()
		}
	}
	items = append(items, toggle)
	return widget.NewCard("", "", container.New(&spacedVBox{gap: 14}, items...))
}

func (u *ui) billingCard() fyne.CanvasObject {
	heading, detail, action := "Free trial", u.allowance(), "Subscribe to Agent"
	if u.paid() {
		heading, detail, action = "Agent subscription", "Your existing address and API keys keep working.", "Manage billing"
		if u.account.BillingState == "grace" {
			heading, detail = "Payment needs attention", "Open billing to update your payment details through Link."
		}
	} else if u.trialFinished() {
		heading = "Your free trial has finished"
		detail = "New API requests are blocked. Subscribe to restore access with the same address and key. Stop sharing below if you want to release model memory."
	} else {
		detail += ". You can subscribe at any time without finishing the trial."
	}
	return widget.NewCard(heading, "", container.NewVBox(u.secondary(detail, fyne.TextAlignLeading), u.primary(action, false, u.billing)))
}

func (u *ui) orbit() fyne.CanvasObject {
	icon := canvas.NewImageFromResource(appIcon)
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(96, 96))
	return container.NewCenter(icon)
}

func (u *ui) title(text string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	label.Wrapping = fyne.TextWrapWord
	label.SizeName = theme.SizeNameHeadingText
	return label
}

func (u *ui) caption(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
}

func (u *ui) secondary(text string, align fyne.TextAlign) *widget.Label {
	label := widget.NewLabelWithStyle(text, align, fyne.TextStyle{})
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.LowImportance
	return label
}

// primary is the filled call-to-action button; it is disabled while working.
func (u *ui) primary(label string, disabled bool, tapped func()) *widget.Button {
	button := widget.NewButton(label, tapped)
	button.Importance = widget.HighImportance
	if disabled || u.working() {
		button.Disable()
	}
	return button
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	value, exponent := float64(n)/unit, 0
	for value >= unit && exponent < 4 {
		value /= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %cB", value, "KMGTP"[exponent])
}
