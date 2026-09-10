In Fyne, the clean way is to listen for the **window resize event** and recalculate the number of grid columns from the available width.

For your image browser, I'd use the window's `Canvas().SetOnTypedKey` only for keyboard events—not resizing. Instead, attach a custom layout/container that reacts to `MinSize`/`Layout`.

### Simplest approach: custom grid layout

Rather than rebuilding the entire grid whenever the window changes size, use a custom `Layout` that calculates the number of columns.

```go
type ThumbnailGridLayout struct {
	ThumbnailWidth float32
}

func (l *ThumbnailGridLayout) Layout(
	objects []fyne.CanvasObject,
	size fyne.Size,
) {
	columns := int(size.Width / l.ThumbnailWidth)

	if columns < 1 {
		columns = 1
	}

	cellWidth := size.Width / float32(columns)

	for i, obj := range objects {
		row := i / columns
		col := i % columns

		obj.Move(fyne.NewPos(
			float32(col)*cellWidth,
			float32(row)*l.ThumbnailWidth,
		))

		obj.Resize(fyne.NewSize(
			cellWidth,
			l.ThumbnailWidth,
		))
	}
}

func (l *ThumbnailGridLayout) MinSize(
	objects []fyne.CanvasObject,
) fyne.Size {
	return fyne.NewSize(
		l.ThumbnailWidth,
		l.ThumbnailWidth,
	)
}
```

Then instead of:

```go
b.grid = container.NewGridWithColumns(b.columns)
```

use:

```go
b.grid = container.New(
	&ThumbnailGridLayout{
		ThumbnailWidth: 240,
	},
)
```

Now Fyne automatically calls `Layout()` when the window/container is resized.

### However, there is a catch

Your thumbnail widget contains:

```text
┌──────────────────┐
│                  │
│    thumbnail     │
│                  │
├──────────────────┤
│ filename.jpg     │
└──────────────────┘
```

so the layout should really account for **thumbnail height + filename height**, rather than treating the entire cell as 240×240.

I'd use something closer to:

```go
type ThumbnailGridLayout struct {
	CellWidth  float32
	CellHeight float32
}

func (l *ThumbnailGridLayout) Layout(
	objects []fyne.CanvasObject,
	size fyne.Size,
) {
	columns := int(size.Width / l.CellWidth)

	if columns < 1 {
		columns = 1
	}

	cellWidth := size.Width / float32(columns)

	for i, obj := range objects {
		row := i / columns
		col := i % columns

		obj.Move(fyne.NewPos(
			float32(col)*cellWidth,
			float32(row)*l.CellHeight,
		))

		obj.Resize(fyne.NewSize(
			cellWidth,
			l.CellHeight,
		))
	}
}

func (l *ThumbnailGridLayout) MinSize(
	objects []fyne.CanvasObject,
) fyne.Size {
	return fyne.NewSize(l.CellWidth, l.CellHeight)
}
```

Then:

```go
b.grid = container.New(
	&ThumbnailGridLayout{
		CellWidth:  220,
		CellHeight: 250,
	},
)
```

### If you want actual responsive thumbnails

I'd go one step further for your application.

Instead of having a fixed:

```go
thumbSize := 220
```

calculate:

```go
columns := int(width / 220)
```

and then:

```go
thumbSize := width / float32(columns)
```

That gives you:

```text
Window width       Columns
──────────────────────────
 800                  3
1000                  4
1200                  5
1400                  6
1600                  7
2000                  9
```

The **layout itself** should do this rather than an explicit resize callback. That's the more idiomatic Fyne approach and avoids resize-event thrashing.

If you specifically want a `resize` callback, Fyne also lets you implement a custom widget/container whose `Resize()` triggers the recalculation, but for this image grid I'd strongly prefer a custom `fyne.Layout`.
