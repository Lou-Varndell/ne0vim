package viewer

import "fyne.io/fyne/v2"

// ThumbnailGridLayout arranges cells in a grid, choosing the column count
// from the container's available width and stretching each cell to fill
// its row exactly, rather than leaving a ragged edge like the built-in
// fyne.io/fyne/v2/layout.GridWrapLayout (which keeps a fixed cell width and
// leaves any leftover space empty).
//
// Because Fyne's Layout interface has no way to report a size-dependent
// MinSize (MinSize is called with no size hint), the row count used for
// MinSize is cached from the most recent Layout call, mirroring the
// approach used by Fyne's own GridWrapLayout.
type ThumbnailGridLayout struct {
	CellWidth  float32
	CellHeight float32
	Gap        float32

	rowCount int
}

// Layout positions objects in a wrapping grid whose column count is
// recalculated from size.Width, so it adapts automatically on window resize.
func (l *ThumbnailGridLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	columns := max(int((size.Width+l.Gap)/(l.CellWidth+l.Gap)), 1)
	cellWidth := (size.Width - float32(columns-1)*l.Gap) / float32(columns)

	rows := 0
	for i, obj := range objects {
		row := i / columns
		col := i % columns
		rows = max(rows, row+1)

		obj.Move(fyne.NewPos(float32(col)*cellWidth, float32(row)*l.CellHeight))
		obj.Resize(fyne.NewSize(cellWidth, l.CellHeight))
	}
	l.rowCount = rows
}

// MinSize reports the height needed for all rows at the last known column
// count, so the enclosing scroll container sizes its content correctly.
func (l *ThumbnailGridLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	rows := max(l.rowCount, 1)
	return fyne.NewSize(l.CellWidth, l.CellHeight*float32(rows))
}

// PreviewRowLayout shows as many of its objects as fit at CellWidth each in
// a single left-aligned row, hiding the rest — the same "count = width /
// CellWidth" rule ThumbnailGridLayout uses per row, but capped to one row
// instead of wrapping to the next. Recalculating on every Layout call means
// the visible count adapts automatically as the window is resized.
type PreviewRowLayout struct {
	CellWidth  float32
	CellHeight float32
	Gap        float32
}

// Layout shows objects[:visible] positioned left to right at CellWidth
// spacing and hides the remainder, where visible is clamped to at least 1
// (so a row is never fully hidden) and at most len(objects).
func (l *PreviewRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}

	visible := min(max(int((size.Width+l.Gap)/(l.CellWidth+l.Gap)), 1), len(objects))

	for i, obj := range objects {
		if i >= visible {
			obj.Hide()
			continue
		}
		obj.Move(fyne.NewPos(float32(i)*(l.CellWidth+l.Gap), 0))
		obj.Resize(fyne.NewSize(l.CellWidth, l.CellHeight))
		obj.Show()
	}
}

// MinSize reports a single cell so this row never forces the page wider
// than the window — it should shrink with the window, not force horizontal
// scrolling of the whole preview.
func (l *PreviewRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(l.CellWidth, l.CellHeight)
}
