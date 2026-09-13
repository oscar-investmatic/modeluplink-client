package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func endpointID(e *endpoint) string {
	if e == nil {
		return ""
	}
	return e.ID
}

func (u *ui) connectionLimitScreen() fyne.CanvasObject {
	e := *u.connectionLimit
	status := "This connection is offline. Its address is still reserved and counts toward your limit."
	if e.Online {
		status = "This connection is online. You can keep using its address and existing API keys."
	}
	return container.NewVBox(
		u.title("You already have a connection"),
		u.secondary("Your account has reached its connection limit. Manage your existing connections before sharing from this computer.", fyne.TextAlignCenter),
		u.secondary(e.URL, fyne.TextAlignCenter),
		u.secondary(status, fyne.TextAlignCenter),
		u.primary("Open dashboard", false, u.dashboard),
		u.primary("Copy existing address", false, func() { u.copy(e.URL, "Address") }),
		u.primary("Refresh connections", false, func() { u.refresh(false) }),
		u.secondary("Removing a connection disables its address and API keys. Stopping sharing leaves the address reserved.", fyne.TextAlignCenter),
	)
}
func (u *ui) keepOtherComputer() {
	if u.working() {
		return
	}
	u.keepingOtherComputer, u.preparingMove = true, false
	u.render()
}
func (u *ui) confirmMove() {
	if u.working() || u.otherTrial == nil {
		return
	}
	id := u.otherTrial.ID
	u.confirm("Move sharing to this computer?", "We’ll prepare your selected model first, then stop sharing on the other computer. You’ll receive a new address and API key. Your remaining trial requests, transfer allowance, and expiry stay the same.", "Move sharing", func() { u.connectWithMove(id) })
}
func (u *ui) otherComputerScreen() fyne.CanvasObject {
	e := *u.otherTrial
	title := "Your trial is already sharing a model from another computer."
	if u.keepingOtherComputer {
		title = "Sharing stays on your other computer."
	}
	status := "That connection is currently offline."
	if e.Online {
		status = "That connection is online."
	}
	items := []fyne.CanvasObject{u.title(title), u.secondary(status, fyne.TextAlignCenter), u.secondary(e.URL, fyne.TextAlignCenter), u.secondary(u.allowance(), fyne.TextAlignCenter)}
	if u.keepingOtherComputer {
		items = append(items, u.primary("Copy address", false, func() { u.copy(e.URL, "Address") }), u.secondary("Use its existing API key. Keep the other computer awake and online.", fyne.TextAlignCenter))
	} else {
		items = append(items, u.primary("Keep using that computer", false, u.keepOtherComputer))
	}
	items = append(items, u.primary("Move sharing to this computer", u.trialFinished(), func() { u.preparingMove = true; u.render() }), u.primary("Open dashboard", false, u.dashboard))
	return container.NewVBox(items...)
}
