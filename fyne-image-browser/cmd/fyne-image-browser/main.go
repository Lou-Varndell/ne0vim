package main

import (
	"flag"
	"log"

	"fyne-image-browser/internal/ui"

	"fyne.io/fyne/v2/app"
)

func main() {
	dir := flag.String("dir", ".", "directory to browse")
	flag.Parse()

	a := app.NewWithID("com.local.imagebrowser")
	w := a.NewWindow("Image Browser")

	browser := ui.NewBrowser(*dir)
	browser.SetWindow(w)
	w.SetContent(browser.Canvas())
	w.Resize(browser.InitialSize())
	w.Show()

	if err := browser.LoadDirectory(*dir); err != nil {
		log.Printf("load directory: %v", err)
	}

	a.Run()
}
