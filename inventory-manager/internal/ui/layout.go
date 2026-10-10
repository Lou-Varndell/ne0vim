package ui

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

	rowCount int
}

// Layout positions objects in a wrapping grid whose column count is
// recalculated from size.Width, so it adapts automatically on window resize.
func (l *ThumbnailGridLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	columns := max(int(size.Width/l.CellWidth), 1)
	cellWidth := size.Width / float32(columns)

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
