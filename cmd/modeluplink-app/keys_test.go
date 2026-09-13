package main

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type unavailableKeys struct{ testKeys }

func (unavailableKeys) read(string) (string, error) {
	return "", errors.New("unlock cancelled")
}

func TestRecoveryMessageIsVisibleWithoutScrolling(t *testing.T) {
	for _, size := range []fyne.Size{{Width: minWidth, Height: minHeight}, {Width: defaultWidth, Height: defaultHeight}} {
		u, _ := newTestUI(t)
		u.window.Resize(size)
		u.account = &account{Email: "test@example.invalid"}
		u.endpoints = []endpoint{{Slug: "test-model", URL: "https://test-model.example.invalid/v1"}}
		u.errText = errKeyringRead.Error()
		u.render()
		var problem *widget.Label
		for _, object := range test.LaidOutObjects(u.content) {
			if label, ok := object.(*widget.Label); ok && label.Text == u.errText {
				problem = label
			}
		}
		if problem == nil {
			t.Fatal("missing recovery message")
		}
		position := u.app.Driver().AbsolutePositionForObject(problem)
		if position.Y < 0 || position.Y+problem.Size().Height > u.window.Canvas().Size().Height {
			t.Fatalf("recovery requires scrolling at %v: position=%v size=%v", size, position, problem.Size())
		}
	}
}

func TestCancelledKeyringUnlockDoesNotCreateKey(t *testing.T) {
	u, h := newTestUI(t)
	u.keys = unavailableKeys{}
	u.copyKey(endpoint{Slug: "test-model"})
	if u.errText != errKeyringRead.Error() {
		t.Fatalf("missing recovery message: %q", u.errText)
	}
	if len(h.calls) != 0 || u.working() {
		t.Fatal("keyring failure started a key creation request")
	}
}
