package main

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestFlatpakSettingsKeepCloseOutsideScrollableContent(t *testing.T) {
	t.Setenv("FLATPAK_ID", appID)
	for _, size := range []fyne.Size{{Width: minWidth, Height: minHeight}, {Width: defaultWidth, Height: defaultHeight}} {
		t.Run(fmt.Sprintf("%gx%g", size.Width, size.Height), func(t *testing.T) {
			u, _ := newTestUI(t)
			u.window.Resize(size)
			desktopSettings(u)[0].(*widget.Button).OnTapped()
			popup := u.window.Canvas().Overlays().Top()
			var body *container.Scroll
			var closeButton *widget.Button
			var visit func(fyne.CanvasObject)
			visit = func(object fyne.CanvasObject) {
				if scroll, ok := object.(*container.Scroll); ok {
					body = scroll
				}
				if button, ok := object.(*widget.Button); ok && button.Text == "Close" {
					closeButton = button
				}
				switch object := object.(type) {
				case *fyne.Container:
					for _, child := range object.Objects {
						visit(child)
					}
				case fyne.Widget:
					for _, child := range test.WidgetRenderer(object).Objects() {
						visit(child)
					}
				}
			}
			visit(popup)
			if body == nil || closeButton == nil {
				t.Fatal("settings must have a scrollable body and an independent Close button")
			}
			bodyPosition := u.app.Driver().AbsolutePositionForObject(body)
			closePosition := u.app.Driver().AbsolutePositionForObject(closeButton)
			if bodyPosition.Y+body.Size().Height > closePosition.Y {
				t.Fatal("settings content overlaps the Close button")
			}
			if closePosition.Y+closeButton.Size().Height > u.window.Canvas().Size().Height {
				t.Fatalf("Close button falls outside the window: position=%v size=%v canvas=%v popup=%v", closePosition, closeButton.Size(), u.window.Canvas().Size(), popup.Size())
			}
			test.Tap(closeButton)
			if u.window.Canvas().Overlays().Top() != nil {
				t.Fatal("Close did not dismiss settings")
			}
		})
	}
}
