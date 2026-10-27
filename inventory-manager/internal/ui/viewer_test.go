package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestViewerSiteEditableOnlyWhenEmpty(t *testing.T) {
	db := openTestDB(t)
	f := createTestFile(t, db, "/photos/a.jpg")

	a := test.NewApp()
	defer test.NewApp() // reset the current app for later tests

	records := []imageRecord{{FileID: f.ID, Path: f.Path}}
	v := newViewer(a.NewWindow(""), db, records, 0, nil)

	if v.siteEntry.Disabled() {
		t.Fatalf("siteEntry should be enabled when site is empty")
	}

	test.Type(v.siteEntry, "example.com")
	v.saveMetadata()

	if !v.siteEntry.Disabled() {
		t.Fatalf("siteEntry should be disabled after a site is saved")
	}
}
