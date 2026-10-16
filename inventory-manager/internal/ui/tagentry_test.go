package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestTagInputSuggestFilterAndSelect(t *testing.T) {
	db := openTestDB(t)
	for _, name := range []string{"cat", "car", "dog"} {
		if _, err := db.GetOrCreateTag(context.Background(), name); err != nil {
			t.Fatalf("seed tag %q: %v", name, err)
		}
	}

	ti := newTagInput(db)
	test.Type(ti.Entry, "ca")

	if !ti.suggestions.Visible() {
		t.Fatalf("suggestions not shown after typing 'ca'")
	}
	if len(ti.suggestions.Objects) != 2 {
		t.Fatalf("suggestions = %d, want 2 (cat, car); got %v", len(ti.suggestions.Objects), ti.suggestions.Objects)
	}

	for _, obj := range ti.suggestions.Objects {
		btn := obj.(*widget.Button)
		if btn.Text == "cat" {
			test.Tap(btn)
		}
	}
	if ti.Text() != "cat" {
		t.Fatalf("Text() after picking cat = %q, want %q", ti.Text(), "cat")
	}
	if ti.suggestions.Visible() {
		t.Fatalf("suggestions should hide after picking")
	}

	// Picking a suggestion must leave the cursor at the end of the
	// completed tag, not wherever it was while the user was still typing
	// the token — otherwise continuing to type inserts into the middle of
	// the value instead of appending the next tag.
	test.Type(ti.Entry, ", do")
	if ti.Text() != "cat, do" {
		t.Fatalf("Text() = %q, want %q", ti.Text(), "cat, do")
	}
	if len(ti.suggestions.Objects) != 1 {
		t.Fatalf("second-token suggestions = %d, want 1 (dog)", len(ti.suggestions.Objects))
	}

	ti.Entry.SetText("")
	test.Type(ti.Entry, "zzz-nomatch")
	if ti.suggestions.Visible() {
		t.Fatalf("suggestions should be hidden when nothing matches")
	}
	if ti.Text() != "zzz-nomatch" {
		t.Fatalf("free typed value lost: %q", ti.Text())
	}
}
