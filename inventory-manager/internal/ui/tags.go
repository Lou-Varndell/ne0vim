package ui

import (
	"context"
	"fmt"

	invlib "inv-lib"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// OpenManageTagsDialog shows a modal dialog for creating and deleting
// tags across the whole database, independent of any single record. The
// browser's grid is refreshed afterward, in case the current tag filter
// is affected by what changed.
func (b *Browser) OpenManageTagsDialog() {
	showManageTagsDialog(b.win, b.store, func() { b.searchAsync() })
}

// showManageTagsDialog is the free-function implementation behind
// Browser.OpenManageTagsDialog, kept separate so the dialog's own state
// (the loaded tag list, the popup handle) doesn't need to live on Browser.
// onChange is called after a tag is created or deleted; it may be nil.
func showManageTagsDialog(parent fyne.Window, store *invlib.DB, onChange func()) {
	if store == nil {
		dialog.ShowInformation("Manage Tags", "No inventory database available.", parent)
		return
	}

	var (
		pop  *widget.PopUp
		list *widget.List
		tags []*invlib.Tag
	)

	reload := func() {
		loaded, err := store.ListTags(context.Background())
		if err != nil {
			dialog.ShowError(err, parent)
			return
		}
		tags = loaded
		if list != nil {
			list.Refresh()
		}
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("New tag name")

	add := func() {
		name := nameEntry.Text
		if name == "" {
			return
		}
		if _, err := store.GetOrCreateTag(context.Background(), name); err != nil {
			dialog.ShowError(err, parent)
			return
		}
		nameEntry.SetText("")
		reload()
		if onChange != nil {
			onChange()
		}
	}
	nameEntry.OnSubmitted = func(string) { add() }
	addBtn := widget.NewButton("Add", add)
	addBtn.Importance = widget.HighImportance

	list = widget.NewList(
		func() int { return len(tags) },
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewLabel(""), widget.NewButton("Delete", nil))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if int(id) < 0 || int(id) >= len(tags) {
				return
			}
			row := obj.(*fyne.Container)
			label := row.Objects[0].(*widget.Label)
			del := row.Objects[1].(*widget.Button)

			tag := tags[id]
			label.SetText(tag.Name)
			del.OnTapped = func() {
				confirmDeleteTag(parent, store, tag, reload, onChange)
			}
		},
	)

	addRow := container.NewBorder(nil, nil, nil, addBtn, nameEntry)
	body := container.NewBorder(addRow, nil, nil, nil, list)

	closeBtn := widget.NewButton("Close", func() { pop.Hide() })
	content := container.NewBorder(
		widget.NewLabelWithStyle("Manage Tags", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		closeBtn, nil, nil,
		body,
	)

	pop = widget.NewModalPopUp(content, parent.Canvas())
	pop.Resize(fyne.NewSize(420, 560))
	reload()
	pop.Show()
}

// OpenBulkTagDialog shows a dialog for adding tags to every file currently
// checked in the grid, in one action. Unlike the viewer's per-file tag
// editor (which replaces a single file's whole tag set), this only adds —
// each file's existing tags are left alone, since the selected files may
// already carry different tags of their own. The selection is cleared and
// the grid re-searched afterward, in case the new tags affect the current
// filter.
func (b *Browser) OpenBulkTagDialog() {
	showBulkTagDialog(b.win, b.store, b.selectedIDs(), func() {
		b.clearSelection()
		b.searchAsync()
	})
}

// showBulkTagDialog is the free-function implementation behind
// Browser.OpenBulkTagDialog, kept separate so the dialog's own state (the
// tags entry, the popup handle) doesn't need to live on Browser.
// onApplied is called after tags are successfully added to every file in
// fileIDs; it may be nil.
func showBulkTagDialog(parent fyne.Window, store *invlib.DB, fileIDs []int64, onApplied func()) {
	if store == nil {
		dialog.ShowInformation("Tag Selected", "No inventory database available.", parent)
		return
	}
	if len(fileIDs) == 0 {
		dialog.ShowInformation("Tag Selected", "No files selected. Check the files you want to tag first.", parent)
		return
	}

	tagsEntry := newTagInput(store)

	content := container.NewVBox(
		widget.NewLabel(fmt.Sprintf("Add tags to %d selected file(s):", len(fileIDs))),
		tagsEntry.CanvasObject(),
	)

	confirm := dialog.NewCustomConfirm("Tag Selected", "Apply", "Cancel", content, func(apply bool) {
		if !apply {
			return
		}
		tags := splitTags(tagsEntry.Text())
		if len(tags) == 0 {
			return
		}
		if err := addTagsToFiles(context.Background(), store, fileIDs, tags); err != nil {
			dialog.ShowError(err, parent)
			return
		}
		if onApplied != nil {
			onApplied()
		}
	}, parent)
	confirm.Show()
}

// confirmDeleteTag asks for confirmation before deleting tag, since
// DeleteTag removes it from every file currently tagged with it.
func confirmDeleteTag(parent fyne.Window, store *invlib.DB, tag *invlib.Tag, reload func(), onChange func()) {
	dialog.ShowConfirm(
		"Delete Tag",
		fmt.Sprintf("Delete tag %q? This removes it from every file it's attached to.", tag.Name),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			if err := store.DeleteTag(context.Background(), tag.ID); err != nil {
				dialog.ShowError(err, parent)
				return
			}
			reload()
			if onChange != nil {
				onChange()
			}
		},
		parent,
	)
}
