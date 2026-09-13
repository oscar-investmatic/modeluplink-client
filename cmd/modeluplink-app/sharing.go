package main

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Uniform gaps keep the entire native button target clear of adjacent actions.
type spacedVBox struct{ gap float32 }

func (l *spacedVBox) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(0, 0)
	count := 0
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		size.Width = max(size.Width, m.Width)
		size.Height += m.Height
		count++
	}
	if count > 1 {
		size.Height += float32(count-1) * l.gap
	}
	return size
}
func (l *spacedVBox) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h + l.gap
	}
}

func (u *ui) editSharing(target endpoint) {
	if u.working() || u.editing {
		return
	}
	u.editing = true
	selected := slices.Clone(u.sharedModels[target.Slug])
	if selected == nil && slices.Contains(u.modelsFor(target), u.selectedModel) {
		selected = []string{u.selectedModel}
	}
	options := slices.Clone(u.modelsFor(target))
	selected = slices.DeleteFunc(selected, func(name string) bool { return !slices.Contains(options, name) })
	var sheet *dialog.CustomDialog
	var choices *widget.CheckGroup
	var multiple *widget.Check
	save := widget.NewButton("Save shared models", func() {
		names := slices.Clone(choices.Selected)
		sheet.Hide()
		u.updateSharing(target, names)
	})
	save.Importance = widget.HighImportance
	choices = widget.NewCheckGroup(options, func(names []string) {
		if multiple != nil && !multiple.Checked && len(names) > 1 {
			// The last changed checkbox is the new single selection.
			latest := names[len(names)-1]
			for _, name := range names {
				if !slices.Contains(selected, name) {
					latest = name
					break
				}
			}
			choices.SetSelected([]string{latest})
			return
		}
		selected = slices.Clone(names)
		if len(names) > 0 && len(names) <= 8 {
			save.Enable()
		} else {
			save.Disable()
		}
	})
	multiple = widget.NewCheck("Share more than one model", func(enabled bool) {
		if !enabled && len(choices.Selected) > 1 {
			choices.SetSelected(choices.Selected[:1])
		}
	})
	multiple.Checked = len(selected) > 1
	choices.SetSelected(selected)
	if len(selected) == 0 {
		save.Disable()
	}
	cancel := widget.NewButton("Cancel", func() { sheet.Hide() })
	note := u.secondary("Only selected models appear at your address. One request runs at a time; up to four wait. Switching models may take longer. Saving checks each model and reconnects using your existing address and key.", fyne.TextAlignLeading)
	body := container.New(&spacedVBox{gap: 14}, multiple, choices, note, container.New(&spacedRow{gap: 16}, cancel, save))
	sheet = dialog.NewCustomWithoutButtons("Shared models", body, u.window)
	sheet.SetOnClosed(func() { u.editing = false })
	sheet.Resize(fyne.NewSize(440, 420))
	sheet.Show()
}

// Action rows use equal, fully clickable targets with a deliberate gap.
type spacedRow struct{ gap float32 }

func (l *spacedRow) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(0, 0)
	count := 0
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		size.Width = max(size.Width, m.Width)
		size.Height = max(size.Height, m.Height)
		count++
	}
	size.Width *= float32(count)
	if count > 1 {
		size.Width += float32(count-1) * l.gap
	}
	return size
}
func (l *spacedRow) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	count := 0
	for _, o := range objects {
		if o.Visible() {
			count++
		}
	}
	if count == 0 {
		return
	}
	width := max(0, (size.Width-float32(count-1)*l.gap)/float32(count))
	x := float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(width, size.Height))
		x += width + l.gap
	}
}
