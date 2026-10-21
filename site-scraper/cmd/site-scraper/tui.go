package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"main/internal/manifest"
)

// Downloader reports progress for three phases, in this order; phaseCount
// also sizes model's per-phase arrays.
const (
	phaseClassify = iota
	phaseDiscover
	phaseDownload
	phaseCount
)

var phaseLabels = [phaseCount]string{"Classifying", "Discovering", "Downloading"}

// pane identifies one of the four boxes in the dashboard grid. All four are
// always visible at once; pane only selects which one is focused for
// keyboard scrolling (Tab/Shift+Tab cycle focus, 1-4 jump directly).
type pane int

const (
	paneProgress pane = iota
	paneActivity
	paneFailures
	paneJSON
	paneCount
)

var paneLabels = [paneCount]string{"Progress", "Activity", "Failures", "JSON"}

// maxLogLines bounds memory for a long run; only the most recent lines matter
// for a live status view.
const maxLogLines = 500

// headerLines and footerLines are the fixed chrome above and below the 2x2
// box grid, used to size the grid (and the viewports inside it) to the
// terminal.
const (
	headerLines = 2
	footerLines = 2
)

// progressMsg mirrors one scraper.With*Progress callback invocation.
type progressMsg struct {
	phase     int
	completed int
	total     int
}

// logMsg is one forwarded slog line (see teaLogWriter).
type logMsg string

// doneMsg carries the terminal result of Downloader.Run.
type doneMsg struct {
	mf  *manifest.Manifest
	err error
}

// entryMsg is one manifest entry streamed live via
// scraper.WithEntryProgress, arriving the moment Run finalizes it rather
// than only once at doneMsg.
type entryMsg manifest.Entry

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("204"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))

	focusedBorderColor   = lipgloss.Color("212")
	unfocusedBorderColor = lipgloss.Color("240")
)

type model struct {
	bars      [phaseCount]progress.Model
	completed [phaseCount]int
	total     [phaseCount]int

	spin  spinner.Model
	focus pane

	// activity is a live tail of every forwarded slog line. failures and
	// jsonView are both rendered from liveEntries as entryMsg streams in
	// during the run (see the entryMsg case in Update), then re-rendered
	// once more from the authoritative final manifest at doneMsg - which
	// also covers entries merged in from a prior run's manifest.json that
	// never passed through entryMsg this run.
	activity    viewport.Model
	failures    viewport.Model
	jsonView    viewport.Model
	logs        []string
	liveEntries []manifest.Entry
	// liveKept/liveDuplicate/liveFailed track liveEntries incrementally
	// rather than rescanning it on every entryMsg.
	liveKept, liveDuplicate, liveFailed int

	width, height int
	ready         bool

	start  time.Time
	cancel func()
	// canceling is set on the first q/ctrl+c while running: it cancels the
	// run's context and keeps the TUI open until Downloader.Run actually
	// returns, so in-flight downloads land and the manifest write stays
	// consistent rather than torn. A second q/ctrl+c force-quits instead of
	// waiting - see the "canceling" branch in Update.
	canceling bool
	// forceQuit is set by a second q/ctrl+c while canceling: run() checks it
	// to skip waiting on the in-flight goroutine and exit immediately.
	forceQuit bool

	finished bool
	result   *manifest.Manifest
	runErr   error
}

func newModel(cancel func()) model {
	m := model{start: time.Now(), cancel: cancel}
	for i := range m.bars {
		m.bars[i] = progress.New(progress.WithDefaultGradient())
	}
	m.spin = spinner.New(spinner.WithSpinner(spinner.Dot))
	m.activity = viewport.New(0, 0)
	m.failures = viewport.New(0, 0)
	m.jsonView = viewport.New(0, 0)
	m.failures.SetContent(renderFailures(nil, false))
	m.jsonView.SetContent(renderManifestJSON(nil))
	return m
}

func (m model) Init() tea.Cmd {
	return m.spin.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.resize()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			m.focus = (m.focus + 1) % paneCount
			return m, nil
		case "shift+tab":
			m.focus = (m.focus - 1 + paneCount) % paneCount
			return m, nil
		case "1", "2", "3", "4":
			m.focus = pane(msg.String()[0] - '1')
			return m, nil

		case "ctrl+c", "q":
			if m.finished {
				return m, tea.Quit
			}
			if m.canceling {
				m.forceQuit = true
				return m, tea.Quit
			}
			m.canceling = true
			m.cancel()
			m.logs = append(m.logs, "canceling... press q again to force quit")
			m.activity.SetContent(renderActivity(m.logs))
			m.activity.GotoBottom()
			return m, nil
		}

		// Not an app-level key: let the active pane's own viewport
		// bindings (arrows, pgup/pgdown, u/d, ...) handle it.
		return m, m.updateActivePane(msg)

	case progressMsg:
		m.completed[msg.phase] = msg.completed
		m.total[msg.phase] = msg.total
		return m, nil

	case logMsg:
		atBottom := m.activity.AtBottom()
		m.logs = append(m.logs, string(msg))
		if len(m.logs) > maxLogLines {
			m.logs = m.logs[len(m.logs)-maxLogLines:]
		}
		m.activity.SetContent(renderActivity(m.logs))
		// Only auto-follow the tail if the viewer hadn't scrolled back to
		// read earlier output; otherwise a burst of log lines would yank
		// them back to the bottom mid-read.
		if atBottom {
			m.activity.GotoBottom()
		}
		return m, nil

	case entryMsg:
		e := manifest.Entry(msg)
		m.liveEntries = append(m.liveEntries, e)
		switch {
		case e.Status == manifest.StatusKept:
			m.liveKept++
		case manifest.IsDuplicate(e.Status):
			m.liveDuplicate++
		default:
			m.liveFailed++
		}

		atBottomF, atBottomJ := m.failures.AtBottom(), m.jsonView.AtBottom()
		m.failures.SetContent(renderFailures(m.liveEntries, false))
		m.jsonView.SetContent(renderManifestJSON(m.liveEntries))
		if atBottomF {
			m.failures.GotoBottom()
		}
		if atBottomJ {
			m.jsonView.GotoBottom()
		}
		return m, nil

	case doneMsg:
		m.finished = true
		m.result = msg.mf
		m.runErr = msg.err
		switch {
		// The final manifest is the authoritative render - it also covers
		// entries merged in from a prior run's manifest.json that never
		// passed through entryMsg this run (see liveEntries' doc comment).
		case m.result != nil:
			m.failures.SetContent(renderFailures(m.result.Entries, true))
			m.jsonView.SetContent(renderManifestJSON(m.result.Entries))
		case m.runErr != nil:
			m.failures.SetContent(errStyle.Render(m.runErr.Error()))
			m.jsonView.SetContent(errStyle.Render(m.runErr.Error()))
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	return m, m.updateActivePane(msg)
}

// updateActivePane forwards msg to whichever viewport currently has focus,
// so its scroll bindings only ever act on the box the user is looking at
// even though all four boxes are visible at once.
func (m *model) updateActivePane(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.focus {
	case paneActivity:
		m.activity, cmd = m.activity.Update(msg)
	case paneFailures:
		m.failures, cmd = m.failures.Update(msg)
	case paneJSON:
		m.jsonView, cmd = m.jsonView.Update(msg)
	}
	return cmd
}

// gridSize returns the total width/height available for the 2x2 box grid,
// and the per-cell sizes split from it: colL+colR == total width,
// rowT+rowB == total height. The right column and bottom row absorb any
// odd remainder so the grid always exactly fills the terminal.
func (m model) gridSize() (colL, colR, rowT, rowB int) {
	gridW := m.width
	if gridW < 20 {
		gridW = 20
	}
	gridH := m.height - headerLines - footerLines
	if gridH < 10 {
		gridH = 10
	}

	colL = gridW / 2
	colR = gridW - colL
	rowT = gridH / 2
	rowB = gridH - rowT
	return colL, colR, rowT, rowB
}

// innerSize returns the content dimensions of a box given its total
// (bordered) size - see renderBox/renderTopBorder for how the border is
// built around it.
func innerSize(totalW, totalH int) (int, int) {
	w, h := totalW-2, totalH-2
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// resize fits each box's viewport (and the progress bars) to its grid cell,
// leaving room for the header/footer chrome rendered around the grid in
// View and each box's own border.
func (m *model) resize() {
	colL, colR, rowT, rowB := m.gridSize()

	m.activity.Width, m.activity.Height = innerSize(colR, rowT)
	m.failures.Width, m.failures.Height = innerSize(colL, rowB)
	m.jsonView.Width, m.jsonView.Height = innerSize(colR, rowB)

	// The progress box's own line format is
	// "<spinner> <label:12> <bar+percent> <count>"; size each bar so that
	// whole line fits the cell exactly instead of word-wrapping and
	// throwing off the grid. bar.Width is the bar's *total* rendered width
	// including its percentage suffix (bubbles/progress renders the fill
	// chars as Width minus the suffix, not Width plus it), and 9 covers
	// the widest count text ("9999/9999"); the rest is spinner+spaces+label.
	innerW, _ := innerSize(colL, rowT)
	const fixedOverhead = 2 + 1 + 12 + 1 + 1 + 9
	barWidth := innerW - fixedOverhead
	if barWidth < 4 {
		barWidth = 4
	}
	for i := range m.bars {
		m.bars[i].Width = barWidth
	}
}

func (m model) View() string {
	if !m.ready {
		return "starting...\n"
	}

	var b strings.Builder

	elapsed := time.Since(m.start).Round(time.Second)
	fmt.Fprintf(&b, "%s  %s", headerStyle.Render("site-scraper"), dimStyle.Render(elapsed.String()))
	switch {
	case m.finished:
		b.WriteString("   " + m.summaryLine())
	case len(m.liveEntries) > 0:
		b.WriteString("   " + dimStyle.Render(fmt.Sprintf("kept=%d duplicate=%d failed=%d",
			m.liveKept, m.liveDuplicate, m.liveFailed)))
	}
	b.WriteString("\n\n")

	b.WriteString(m.gridView())

	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(m.footerHint()))
	return b.String()
}

// gridView renders all four boxes - Progress, Activity, Failures, JSON - as
// a 2x2 grid, all visible at once. m.focus only controls which one's border
// is highlighted and which one receives scroll keys.
func (m model) gridView() string {
	colL, colR, rowT, rowB := m.gridSize()

	top := lipgloss.JoinHorizontal(lipgloss.Top,
		renderBox("1 Progress", colL, rowT, m.focus == paneProgress, m.progressView()),
		renderBox("2 Activity", colR, rowT, m.focus == paneActivity, m.activity.View()),
	)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top,
		renderBox("3 Failures", colL, rowB, m.focus == paneFailures, m.failures.View()),
		renderBox("4 JSON", colR, rowB, m.focus == paneJSON, m.jsonView.View()),
	)
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

func (m model) footerHint() string {
	if !m.finished && m.canceling {
		return errStyle.Render("canceling, waiting for in-flight work to finish...  ·  q: force quit")
	}
	quit := "q: cancel"
	if m.finished {
		quit = "q: quit"
	}
	return "tab/shift+tab: focus  ·  1-4: jump  ·  ↑/↓ pgup/pgdown: scroll  ·  " + quit
}

func (m model) progressView() string {
	lines := make([]string, len(phaseLabels))
	for i, label := range phaseLabels {
		total := m.total[i]
		completed := m.completed[i]

		var pct float64
		if total > 0 {
			pct = float64(completed) / float64(total)
		}

		count := dimStyle.Render(fmt.Sprintf("%d/%d", completed, total))
		if total == 0 {
			count = dimStyle.Render("pending")
		}

		lines[i] = fmt.Sprintf("%s %-12s %s %s",
			m.spin.View(), label, m.bars[i].ViewAs(pct), count)
	}
	return strings.Join(lines, "\n")
}

// renderBox draws one dashboard box: a custom top border line with the
// title embedded in it (lipgloss has no built-in border-title support), and
// the left/right/bottom sides plus content from lipgloss for everything
// else. MaxWidth/MaxHeight clamp the result to exactly totalW x totalH even
// if content would otherwise word-wrap past it (lipgloss's Width/Height
// pad short content but don't clip long content on their own).
func renderBox(title string, totalW, totalH int, focused bool, content string) string {
	color := unfocusedBorderColor
	if focused {
		color = focusedBorderColor
	}

	innerW, innerH := innerSize(totalW, totalH)
	body := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(color).
		Width(innerW).
		Height(innerH).
		MaxWidth(totalW).
		MaxHeight(totalH - 1).
		Render(content)

	return renderTopBorder(title, totalW, color) + "\n" + body
}

// renderTopBorder builds a box's top border line from scratch (rather than
// splicing a title into lipgloss's own rendered border, which wraps the
// whole line in one ANSI sequence) so the title can be styled distinctly
// from the border color while the line still totals exactly width columns.
func renderTopBorder(title string, width int, color lipgloss.Color) string {
	borderStyle := lipgloss.NewStyle().Foreground(color)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	avail := width - 2
	if avail < 0 {
		avail = 0
	}

	label := " " + title + " "
	for lipgloss.Width(label) > avail && len(label) > 0 {
		label = label[:len(label)-1]
	}

	fill := avail - lipgloss.Width(label)
	left := 1
	if left > fill {
		left = fill
	}
	right := fill - left
	if right < 0 {
		right = 0
	}

	return borderStyle.Render("╭"+strings.Repeat("─", left)) +
		titleStyle.Render(label) +
		borderStyle.Render(strings.Repeat("─", right)+"╮")
}

func (m model) summaryLine() string {
	if m.runErr != nil && m.result == nil {
		return errStyle.Render(fmt.Sprintf("run failed: %v", m.runErr))
	}

	kept, failed, duplicate := 0, 0, 0
	for _, e := range m.result.Entries {
		switch {
		case e.Status == manifest.StatusKept:
			kept++
		case manifest.IsDuplicate(e.Status):
			duplicate++
		default:
			failed++
		}
	}
	return okStyle.Render("done.") + fmt.Sprintf("  kept=%d duplicate=%d failed=%d", kept, duplicate, failed)
}

// renderActivity styles each forwarded slog line by level so warnings and
// errors stand out while scrolling through the tail.
func renderActivity(logs []string) string {
	if len(logs) == 0 {
		return dimStyle.Render("(no activity yet)")
	}

	styled := make([]string, len(logs))
	for i, line := range logs {
		switch {
		case strings.Contains(line, "level=ERROR"):
			styled[i] = errStyle.Render(line)
		case strings.Contains(line, "level=WARN"):
			styled[i] = warnStyle.Render(line)
		default:
			styled[i] = line
		}
	}
	return strings.Join(styled, "\n")
}

// entryKind labels a non-kept entry for display - the only two statuses
// Run ever produces besides StatusKept itself (see manifest.FailedStatus/
// DuplicateStatus).
func entryKind(e manifest.Entry) string {
	if manifest.IsDuplicate(e.Status) {
		return "duplicate"
	}
	return "failed"
}

// renderFailures lists every entry that didn't survive as a kept file -
// failed downloads and duplicates - updating live as entries stream in via
// entryMsg during the run (finished=false) and once more, authoritatively,
// from the final manifest at doneMsg (finished=true).
func renderFailures(entries []manifest.Entry, finished bool) string {
	var blocks []string
	for _, e := range entries {
		if e.Status == manifest.StatusKept {
			continue
		}

		label := errStyle.Render("failed")
		if manifest.IsDuplicate(e.Status) {
			label = warnStyle.Render("duplicate")
		}

		detail := e.Status
		if e.Error != "" {
			detail = e.Error
		}
		blocks = append(blocks, fmt.Sprintf("[%s] %s\n  %s", label, e.SourceURL, detail))
	}

	if len(blocks) == 0 {
		if finished {
			return okStyle.Render("no failures or duplicates")
		}
		return dimStyle.Render("(no failures yet)")
	}
	return strings.Join(blocks, "\n\n")
}

// renderManifestJSON pretty-prints entries the same shape run() encodes to
// stdout ({"entries": [...]}), so the TUI's JSON pane matches the
// file/stdout output exactly - whether entries is the live, still-growing
// list or the final manifest's.
func renderManifestJSON(entries []manifest.Entry) string {
	data, err := json.MarshalIndent(manifest.Manifest{Entries: entries}, "", "  ")
	if err != nil {
		return errStyle.Render(fmt.Sprintf("encode manifest: %v", err))
	}
	return string(data)
}

// teaLogWriter adapts an io.Writer to forward each formatted slog line to
// the running Program as a logMsg instead of writing it to stderr, which
// would otherwise corrupt the TUI's alt-screen rendering.
type teaLogWriter struct {
	program *tea.Program
}

func (w teaLogWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if line != "" {
		w.program.Send(logMsg(line))
	}
	return len(p), nil
}
