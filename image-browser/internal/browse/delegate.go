package browse

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type recordItemDelegate struct {
	list.DefaultDelegate
}

func newRecordItemDelegate() recordItemDelegate {
	d := list.NewDefaultDelegate()
	d.SetHeight(4)
	return recordItemDelegate{DefaultDelegate: d}
}

func (d recordItemDelegate) Height() int {
	return 4
}

func (d recordItemDelegate) Render(
	w io.Writer,
	m list.Model,
	index int,
	item list.Item,
) {
	i, ok := item.(recordItem)
	if !ok {
		return
	}

	titleStyle := d.Styles.NormalTitle
	descStyle := d.Styles.NormalDesc
	if index == m.Index() {
		titleStyle = d.Styles.SelectedTitle
		descStyle = d.Styles.SelectedDesc
	}

	width := m.Width()
	if width < 1 {
		width = 1
	}

	title := titleStyle.Width(width).Render(i.Title())

	desc := strings.Split(i.Description(), "\n")
	var lines []string
	for _, line := range desc {
		lines = append(lines, descStyle.Width(width).Render(line))
	}

	// The list needs a fixed height for every item.
	// Show the title, dimensions, site, and directory.
	output := []string{title}
	output = append(output, lines...)

	if len(output) > 4 {
		output = output[:4]
	}
	fmt.Fprint(w, strings.Join(output, "\n"))
}

func (d recordItemDelegate) Update(
	msg tea.Msg,
	m *list.Model,
) tea.Cmd {
	return d.DefaultDelegate.Update(msg, m)
}
