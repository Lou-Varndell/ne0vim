package browse

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	invlib "inv-lib"
)

// makeRecord builds a minimal *invlib.FileRecord for Update tests that
// don't touch the database — just enough fields for recordItem and
// renderDetail to read.
func makeRecord(id int64, path, site string) *invlib.FileRecord {
	return &invlib.FileRecord{
		File: &invlib.File{ID: id, Path: path, Filename: path},
		Origin: &invlib.Origin{
			ID:   id,
			Type: "scrape",
			Site: &site,
		},
	}
}

// testModel returns a model sized as if a real terminal had already sent
// a WindowSizeMsg, with inv left nil — safe as long as a test never
// invokes the tea.Cmd a given Update call returns (loadRecordsCmd and
// performMoveCmd both dereference inv only once actually called).
func testModel() model {
	m := newModel(context.Background(), func() {}, nil, "/root")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(model)
}

func withRecords(m model, records ...*invlib.FileRecord) model {
	updated, _ := m.updateList(recordsLoadedMsg{records: records})
	return updated.(model)
}

func TestRecordsLoadedPopulatesListAndClearsLoading(t *testing.T) {
	m := testModel()
	if !m.loading {
		t.Fatal("expected loading to start true")
	}

	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"), makeRecord(2, "/root/b.jpg", "site-b"))

	if m.loading {
		t.Fatal("loading = true after recordsLoadedMsg, want false")
	}
	if len(m.items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(m.items))
	}
	if len(m.list.Items()) != 2 {
		t.Fatalf("len(list.Items()) = %d, want 2", len(m.list.Items()))
	}
}

func TestRecordsLoadedErrorGoesToFatalScreen(t *testing.T) {
	m := testModel()

	updated, _ := m.updateList(recordsLoadedMsg{err: errBoom})
	m = updated.(model)

	if m.screen != screenFatalError {
		t.Fatalf("screen = %v, want screenFatalError", m.screen)
	}
	if m.loadErr != errBoom {
		t.Fatalf("loadErr = %v, want %v", m.loadErr, errBoom)
	}
}

func TestToggleSelectsCurrentItem(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(model)

	selected := m.selectedRecords()
	if len(selected) != 1 || selected[0].File.ID != 1 {
		t.Fatalf("selectedRecords() = %v, want [record 1]", selected)
	}

	// Toggling again deselects.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(model)
	if len(m.selectedRecords()) != 0 {
		t.Fatalf("selectedRecords() after second toggle = %v, want none", m.selectedRecords())
	}
}

func TestEnterOpensDetailForSelectedItem(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want screenDetail", m.screen)
	}
	if m.detailRecord == nil || m.detailRecord.File.ID != 1 {
		t.Fatalf("detailRecord = %v, want record 1", m.detailRecord)
	}
}

func TestBackFromDetailReturnsToList(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
}

func TestMoveWithNoSelectionStaysOnList(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = updated.(model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList (nothing selected)", m.screen)
	}
}

func TestMoveWithSelectionOpensDestInput(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = updated.(model)

	if m.screen != screenDestInput {
		t.Fatalf("screen = %v, want screenDestInput", m.screen)
	}
	if !m.destInput.Focused() {
		t.Fatal("expected destInput to be focused after opening the move prompt")
	}
}

func TestDestInputEnterWithEmptyValueDoesNotMove(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = updated.(model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	if m.screen != screenDestInput {
		t.Fatalf("screen = %v, want screenDestInput (empty destination should not proceed)", m.screen)
	}
	if cmd != nil {
		t.Fatal("expected no command for an empty destination")
	}
}

func TestDestInputEnterWithValueStartsMove(t *testing.T) {
	m := testModel()
	m = withRecords(m, makeRecord(1, "/root/a.jpg", "site-a"))
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = updated.(model)

	for _, r := range "/tmp/dest" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(model)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	if m.screen != screenMoving {
		t.Fatalf("screen = %v, want screenMoving", m.screen)
	}
	if cmd == nil {
		t.Fatal("expected a command to perform the move")
	}
}

func TestMoveDoneMsgShowsResult(t *testing.T) {
	m := testModel()
	m.screen = screenMoving

	updated, _ := m.Update(moveDoneMsg{moved: 2, duplicates: 1})
	m = updated.(model)

	if m.screen != screenResult {
		t.Fatalf("screen = %v, want screenResult", m.screen)
	}
	if m.moveResult.moved != 2 || m.moveResult.duplicates != 1 {
		t.Fatalf("moveResult = %+v, want moved=2 duplicates=1", m.moveResult)
	}
}

func TestResultScreenAnyKeyReloadsList(t *testing.T) {
	m := testModel()
	m.screen = screenResult
	m.items = []recordItem{newRecordItem(makeRecord(1, "/root/a.jpg", "site-a"))}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
	if !m.loading {
		t.Fatal("expected loading = true while the list reloads")
	}
	if len(m.items) != 0 {
		t.Fatalf("len(items) = %d, want 0 (cleared pending reload)", len(m.items))
	}
	if cmd == nil {
		t.Fatal("expected a reload command")
	}
}

func TestCtrlCAlwaysQuitsRegardlessOfScreen(t *testing.T) {
	cancelled := false
	m := newModel(context.Background(), func() { cancelled = true }, nil, "/root")

	for _, scr := range []screen{screenList, screenDetail, screenDestInput, screenMoving, screenResult, screenFatalError} {
		m.screen = scr
		updated, cmd := m.Update(tea.KeyPressMsg{
			Code: 'c',
			Mod:  tea.ModCtrl,
		})
		m = updated.(model)

		if cmd == nil {
			t.Fatalf("screen %v: expected a command for ctrl+c", scr)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("screen %v: ctrl+c command did not produce tea.QuitMsg", scr)
		}
	}

	if !cancelled {
		t.Fatal("expected ctrl+c to call the model's cancel func")
	}
}

// errBoom is a stand-in error for tests that don't care about a specific
// failure, just that one occurred.
var errBoom = errTest("boom")

type errTest string

func (e errTest) Error() string { return string(e) }
