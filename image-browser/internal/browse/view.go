package browse

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// View renders whichever screen is active. Every branch composes the same
// three-part layout — title, body, help/status line — so switching screens
// doesn't jump the content around on the terminal.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	switch m.screen {
	case screenFatalError:
		return fmt.Sprintf(
			"%s\n\n%s\n\n%s",
			titleStyle.Render("image-browser"),
			errorStyle.Render("Failed to load the inventory database: "+m.loadErr.Error()),
			helpStyle.Render("ctrl+c to quit"),
		)

	case screenDetail:
		return fmt.Sprintf(
			"%s\n%s\n%s",
			titleStyle.Render("Details"),
			m.detail.View(),
			helpStyle.Render("esc/q back  ·  ctrl+c quit"),
		)

	case screenDestInput:
		n := len(m.selectedRecords())
		return fmt.Sprintf(
			"%s\n\n%s\n%s\n\n%s",
			titleStyle.Render("image-browser"),
			promptStyle.Render(fmt.Sprintf("Move %d selected file(s) to:", n)),
			m.destInput.View(),
			helpStyle.Render("enter confirm  ·  esc cancel  ·  ctrl+c quit"),
		)

	case screenMoving:
		return fmt.Sprintf(
			"%s\n\n%s %s\n",
			titleStyle.Render("image-browser"),
			m.spin.View(),
			"Moving selected file(s)...",
		)

	case screenResult:
		var b strings.Builder
		fmt.Fprintf(&b, "Moved %d; duplicates %d", m.moveResult.moved, m.moveResult.duplicates)
		if len(m.moveResult.failures) > 0 {
			fmt.Fprintf(&b, "; failed %d", len(m.moveResult.failures))
		}
		b.WriteByte('\n')
		for _, f := range m.moveResult.failures {
			fmt.Fprintf(&b, "  %s\n", errorStyle.Render(f))
		}
		if m.moveResult.err != nil {
			fmt.Fprintf(&b, "  %s\n", errorStyle.Render(m.moveResult.err.Error()))
		}

		return fmt.Sprintf(
			"%s\n\n%s\n%s",
			titleStyle.Render("image-browser"),
			b.String(),
			helpStyle.Render("press any key to return to the list"),
		)

	default: // screenList
		if m.loading {
			return fmt.Sprintf(
				"%s\n\n%s Loading inventory...\n",
				titleStyle.Render("image-browser"),
				m.spin.View(),
			)
		}

		n := len(m.selectedRecords())
		help := "space select  ·  enter details  ·  / filter  ·  m move selected  ·  q/esc quit  ·  ctrl+c quit"
		if n > 0 {
			help = fmt.Sprintf("%d selected  ·  ", n) + help
		}

		return fmt.Sprintf(
			"%s\n%s\n",
			m.list.View(),
			helpStyle.Render(help),
		)
	}
}
