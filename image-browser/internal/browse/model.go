package browse

import (
	"context"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	invlib "inv-lib"
)

// screen identifies which of browse's views is currently active. Exactly
// one is shown at a time; Update dispatches by screen, and View renders
// only the active one (plus a persistent header/footer).
type screen int

const (
	screenList       screen = iota // browsing/selecting records
	screenDetail                   // full metadata for one record
	screenDestInput                // entering a destination for a move
	screenMoving                   // move in flight
	screenResult                   // move finished; showing a summary
	screenFatalError               // the initial load failed; nothing else usable
)

// model is browse's root Bubble Tea model. It owns one inv-lib connection
// and a context threaded into every tea.Cmd that does I/O, so a command in
// flight can be cancelled (see browse.go, where cancel is wired to the
// program's own teardown) instead of leaking past the point the user quit.
type model struct {
	ctx    context.Context
	cancel context.CancelFunc
	inv    *invlib.DB
	root   string

	screen screen
	width  int
	height int

	loading bool
	loadErr error

	list  list.Model
	items []recordItem // parallel to the records behind list's items, for selection bookkeeping

	detail       viewport.Model
	detailRecord *invlib.FileRecord

	destInput textinput.Model

	spin spinner.Model

	moveResult moveDoneMsg
	moveErr    error
}

// newModel constructs the initial model. inv and ctx are supplied by
// browse.Run rather than built here, per the retrofit principle of
// reusing what the CLI already set up instead of having the TUI construct
// its own connection.
func newModel(ctx context.Context, cancel context.CancelFunc, inv *invlib.DB, root string) model {
	delegate := newRecordItemDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "image-browser"
	l.SetShowHelp(false)
	l.SetStatusBarItemName("file", "files")

	di := textinput.New()
	di.Placeholder = "destination directory"
	di.Prompt = "dest> "

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))

	return model{
		ctx:       ctx,
		cancel:    cancel,
		inv:       inv,
		root:      root,
		screen:    screenList,
		loading:   true,
		list:      l,
		detail:    viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		destInput: di,
		spin:      sp,
	}
}

// Init kicks off the initial record load and starts the spinner ticking;
// the spinner is reused across every screen that shows one (initial load
// and an in-flight move) rather than constructing a fresh one each time.
func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadRecordsCmd(m.ctx, m.inv, m.root),
		m.spin.Tick,
	)
}

// selectedRecords returns every record currently flagged for a move, in
// list order.
func (m model) selectedRecords() []*invlib.FileRecord {
	var out []*invlib.FileRecord
	for _, it := range m.items {
		if *it.selected {
			out = append(out, it.rec)
		}
	}
	return out
}
