package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// checkTap is a fully transparent widget meant to be stacked directly over
// a widget.Check, at the same position and size (via container.NewStack),
// to own its click handling instead of letting it toggle itself.
//
// widget.Check's own Tapped(*fyne.PointEvent) carries no keyboard-modifier
// state, and there's no way to add that by wrapping Check: embedding
// *widget.Check in a new type breaks it, because Check.CreateRenderer
// hard-codes re-registering itself — not the wrapper — as its BaseWidget
// "super" every time a renderer is (re)created. That leaves the
// container's render pass and the wrapper's later Refresh calls pointed
// at two different cached renderers, so the checkbox stops visually
// updating. Capturing the click on this separate overlay instead, then
// driving the real (untouched) Check via SetChecked, sidesteps that
// entirely.
type checkTap struct {
	widget.BaseWidget
	modifier fyne.KeyModifier
	onTap    func(modifier fyne.KeyModifier)
}

// newCheckTap builds an invisible overlay whose click handling is fully
// owned by onTap: it decides the resulting checked state itself (and, for
// a shift-click, every other card's state too) and calls the underlying
// Check's SetChecked directly.
func newCheckTap(onTap func(modifier fyne.KeyModifier)) *checkTap {
	c := &checkTap{onTap: onTap}
	c.ExtendBaseWidget(c)
	return c
}

// MouseDown records the modifier held for the click Tapped is about to
// report; fyne.PointEvent carries no modifier state of its own.
func (c *checkTap) MouseDown(ev *desktop.MouseEvent) {
	c.modifier = ev.Modifier
}

// MouseUp is required to satisfy desktop.Mouseable; the click is fully
// handled in Tapped.
func (c *checkTap) MouseUp(*desktop.MouseEvent) {}

// Tapped reports the click's modifier to onTap.
func (c *checkTap) Tapped(*fyne.PointEvent) {
	mod := c.modifier
	c.modifier = 0
	if c.onTap != nil {
		c.onTap(mod)
	}
}

// CreateRenderer renders nothing visible — this widget exists only to
// intercept the click; the stock Check beneath it (same stack position)
// provides the actual checkbox appearance.
func (c *checkTap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
