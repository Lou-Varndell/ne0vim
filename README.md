# go-photo — File Dialog Iteration 7

This iteration targets the remaining Fyne file-picker rendering bug where the
breadcrumb changes to the new directory but pooled list rows still show entries
from a previous directory.

## Changes

- Rebuild the directory `widget.List` whenever the directory contents change.
- Build a fresh `[]fyne.URI` before replacing the current data slice.
- Keep the existing immediate folder navigation behavior from iteration 6.
- Keep the current folder selected in folder mode so **Open** chooses it.
- Keep Cmd+Shift+G (Go to Folder).
- Keep Cmd+Shift+. (show/hide hidden entries).
- Keep type-ahead navigation.

The important change is that a directory change no longer reuses Fyne's pooled
list rows. Each directory view gets a fresh list, so a row ID cannot retain the
visual contents of the previous directory.
