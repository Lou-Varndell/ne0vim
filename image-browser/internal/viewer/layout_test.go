package viewer

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

func TestThumbnailGridLayout(t *testing.T) {
	layout := &ThumbnailGridLayout{CellWidth: 150, CellHeight: 170, Gap: 10}
	objects := make([]fyne.CanvasObject, 17)
	for i := range objects {
		objects[i] = canvas.NewRectangle(color.Transparent)
	}

	layout.Layout(objects, fyne.NewSize(900, 500))

	if got := layout.rowCount; got != 3 {
		t.Fatalf("rowCount = %d, want 3", got)
	}
	// 900px fits five 150px cells with four 10px gaps. The remaining
	// width is distributed equally across the five cells.
	wantWidth := (float32(900) - 4*10) / 5
	for i, obj := range objects {
		if got := obj.Size().Width; got != wantWidth {
			t.Errorf("object %d width = %v, want %v", i, got, wantWidth)
		}
		if got := obj.Size().Height; got != 170 {
			t.Errorf("object %d height = %v, want 170", i, got)
		}
	}
	if got := objects[5].Position().X; got != 0 {
		t.Errorf("sixth object X = %v, want 0", got)
	}
	if got := objects[6].Position().Y; got != 170 {
		t.Errorf("seventh object Y = %v, want 170", got)
	}
	if got := layout.MinSize(objects).Height; got != 510 {
		t.Errorf("MinSize height = %v, want 510", got)
	}
}

func TestThumbnailGridLayoutStretchesCells(t *testing.T) {
	layout := &ThumbnailGridLayout{CellWidth: 150, CellHeight: 170, Gap: 10}
	objects := make([]fyne.CanvasObject, 6)
	for i := range objects {
		objects[i] = canvas.NewRectangle(color.Transparent)
	}

	layout.Layout(objects, fyne.NewSize(1000, 500))
	want := (float32(1000) - 5*10) / 6
	for i, obj := range objects {
		if got := obj.Size().Width; got != want {
			t.Errorf("object %d width = %v, want %v", i, got, want)
		}
	}
}

func TestThumbnailRowLayoutShowsOnlyWhatFits(t *testing.T) {
	layout := &PreviewRowLayout{CellWidth: 150, CellHeight: 170, Gap: 10}
	objects := make([]fyne.CanvasObject, 9)
	for i := range objects {
		objects[i] = canvas.NewRectangle(color.Transparent)
	}

	layout.Layout(objects, fyne.NewSize(1000, 170))

	for i, obj := range objects {
		if i < 6 {
			if obj.Size().Width != 150 || obj.Size().Height != 170 {
				t.Errorf("object %d size = %v, want 150x170", i, obj.Size())
			}
			if !obj.Visible() {
				t.Errorf("object %d hidden, want visible", i)
			}
		} else if obj.Visible() {
			t.Errorf("object %d visible, want hidden", i)
		}
	}
}

func TestPreviewRowLayoutGap(t *testing.T) {
	layout := &PreviewRowLayout{CellWidth: 150, CellHeight: 170, Gap: 10}
	objects := make([]fyne.CanvasObject, 6)
	for i := range objects {
		objects[i] = canvas.NewRectangle(color.Transparent)
	}

	layout.Layout(objects, fyne.NewSize(490, 200))

	// 490px fits three 150px cells with two 10px gaps. The fourth cell is hidden.
	for i := 0; i < 3; i++ {
		if !objects[i].Visible() {
			t.Errorf("object %d is hidden, want visible", i)
		}
	}
	if objects[3].Visible() {
		t.Error("object 3 is visible, want hidden")
	}
	if got := objects[1].Position().X; got != 160 {
		t.Errorf("second object X = %v, want 160", got)
	}
	if got := objects[2].Position().X; got != 320 {
		t.Errorf("third object X = %v, want 320", got)
	}
}
