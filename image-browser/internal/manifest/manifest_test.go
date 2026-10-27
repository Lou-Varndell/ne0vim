package manifest

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDecodeAcceptsBareArray(t *testing.T) {
	got, err := Decode([]byte(`[{"site":"s","source_url":"u","file":"a.jpg","status":"processed"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Site: "s", SourceURL: "u", File: "a.jpg", Status: "processed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeAcceptsSingleObject(t *testing.T) {
	got, err := Decode([]byte(`{"site":"s","source_url":"u","file":"a.jpg","status":"kept"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Site: "s", SourceURL: "u", File: "a.jpg", Status: "kept"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeAcceptsEntriesWrapper(t *testing.T) {
	got, err := Decode([]byte(`{"entries":[
		{"site":"s","source_url":"u1","file":"a.jpg","status":"kept"},
		{"site":"s","source_url":"u2","file":"b.jpg","status":"kept"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Site: "s", SourceURL: "u1", File: "a.jpg", Status: "kept"},
		{Site: "s", SourceURL: "u2", File: "b.jpg", Status: "kept"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeAcceptsEmptyEntriesWrapper(t *testing.T) {
	got, err := Decode([]byte(`{"entries":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Decode() = %#v, want empty", got)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Fatal("Decode() err = nil, want error")
	}
}

func TestInTreeAcceptsBareFilenameAndSubdirectory(t *testing.T) {
	base := "/images/gallery"
	cases := []struct {
		file string
		want bool
	}{
		{"photo.jpg", true},
		{filepath.Join(base, "photo.jpg"), true},
		{filepath.Join(base, "sub", "photo.jpg"), true},
		{filepath.Join(filepath.Dir(base), "other", "photo.jpg"), false},
		{filepath.Dir(base) + "/galleryx/photo.jpg", false},
	}
	for _, c := range cases {
		if got := InTree(c.file, base); got != c.want {
			t.Errorf("InTree(%q, %q) = %v, want %v", c.file, base, got, c.want)
		}
	}
}
