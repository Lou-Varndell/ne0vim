package browse

import "charm.land/bubbles/v2/key"

// keyMap centralizes browse's own keybindings (as opposed to the ones
// list.Model and viewport.Model already bind themselves) so the on-screen
// help text in view.go can't drift out of sync with what Update actually
// checks for.
type keyMap struct {
	Toggle key.Binding
	Enter  key.Binding
	Back   key.Binding
	Move   key.Binding
	Quit   key.Binding
}

var keys = keyMap{
	Toggle: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
	Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
	Back:   key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "back")),
	Move:   key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "move selected")),
	Quit:   key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
}
