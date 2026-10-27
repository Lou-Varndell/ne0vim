package browse

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// headerHeight and footerHeight are how many lines the persistent title
// and help/status lines (see view.go) take up, so the list/viewport get
// exactly what's left of the terminal rather than overflowing it.
const (
	headerHeight = 2
	footerHeight = 2
)

// Update is the root dispatch: a global ctrl+c (so the program is always
// escapable no matter which screen or text field has focus) and window
// resize are handled here regardless of screen, then everything else is
// routed to the active screen's own handler.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		contentHeight := max(msg.Height-headerHeight-footerHeight, 0)
		m.list.SetSize(msg.Width, contentHeight)
		m.detail.SetWidth(msg.Width)
		m.detail.SetHeight(contentHeight)
		return m, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	switch m.screen {
	case screenList:
		return m.updateList(msg)
	case screenDetail:
		return m.updateDetail(msg)
	case screenDestInput:
		return m.updateDestInput(msg)
	case screenMoving:
		return m.updateMoving(msg)
	case screenResult:
		return m.updateResult(msg)
	default: // screenFatalError
		return m, nil
	}
}

// updateList handles the main browse/select screen.
func (m model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case recordsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err
			m.screen = screenFatalError
			return m, nil
		}

		m.items = make([]recordItem, len(msg.records))
		listItems := make([]list.Item, len(msg.records))
		for i, rec := range msg.records {
			item := newRecordItem(rec)
			m.items[i] = item
			listItems[i] = item
		}
		cmd := m.list.SetItems(listItems)
		return m, cmd

	case tea.KeyPressMsg:
		// While the user is typing into list's own fuzzy-filter box, every
		// keystroke belongs to that box, not to browse's own bindings below
		// (space/enter/m would otherwise get typed into the filter and
		// simultaneously trigger a screen change or selection toggle).
		if m.list.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

		switch {
		case msg.Code == tea.KeySpace || key.Matches(msg, keys.Toggle):
			if item, ok := m.list.SelectedItem().(recordItem); ok {
				*item.selected = !*item.selected
			}
			return m, nil

		case key.Matches(msg, keys.Enter):
			if item, ok := m.list.SelectedItem().(recordItem); ok {
				m.detailRecord = item.rec
				m.detail.SetContent(renderDetail(item.rec))
				m.detail.GotoTop()
				m.screen = screenDetail
			}
			return m, nil

		case key.Matches(msg, keys.Move):
			if selected := m.selectedRecords(); len(selected) > 0 {
				m.destInput.SetValue(defaultMoveDest(selected))
				m.destInput.CursorEnd()
				m.destInput.Focus()
				m.screen = screenDestInput
				return m, textinput.Blink
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// updateDetail handles the single-record metadata screen.
func (m model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && key.Matches(keyMsg, keys.Back) {
		m.screen = screenList
		return m, nil
	}

	var cmd tea.Cmd
	m.detail, cmd = m.detail.Update(msg)
	return m, cmd
}

// updateDestInput handles the move-destination text prompt.
func (m model) updateDestInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "esc":
			m.destInput.Blur()
			m.screen = screenList
			return m, nil

		case "enter":
			dest := m.destInput.Value()
			if dest == "" {
				return m, nil
			}
			m.destInput.Blur()
			m.screen = screenMoving
			return m, performMoveCmd(m.ctx, m.inv, m.selectedRecords(), dest)
		}
	}

	var cmd tea.Cmd
	m.destInput, cmd = m.destInput.Update(msg)
	return m, cmd
}

// updateMoving handles the in-flight move screen; the only thing that can
// happen here besides the global ctrl+c is the move finishing.
func (m model) updateMoving(msg tea.Msg) (tea.Model, tea.Cmd) {
	done, ok := msg.(moveDoneMsg)
	if !ok {
		return m, nil
	}

	m.moveResult = done
	m.screen = screenResult
	return m, nil
}

// updateResult handles the move-summary screen: any key returns to the
// list, reloading it since the move just changed what's on disk under
// root (moved files, and their paths, no longer match what's cached in
// m.items).
func (m model) updateResult(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.screen = screenList
		m.loading = true
		m.items = nil
		return m, loadRecordsCmd(m.ctx, m.inv, m.root)
	}
	return m, nil
}
