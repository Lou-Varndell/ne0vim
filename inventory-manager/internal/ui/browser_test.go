package ui

import "testing"

func TestSameFileSequence(t *testing.T) {
	a := []imageRecord{{FileID: 1}, {FileID: 2}}

	cases := []struct {
		name string
		b    []imageRecord
		want bool
	}{
		{"identical", []imageRecord{{FileID: 1}, {FileID: 2}}, true},
		{"different length", []imageRecord{{FileID: 1}}, false},
		{"different order", []imageRecord{{FileID: 2}, {FileID: 1}}, false},
		{"different ids", []imageRecord{{FileID: 1}, {FileID: 3}}, false},
		{"both empty", nil, true},
	}

	for _, c := range cases {
		if c.name == "both empty" {
			if got := sameFileSequence(nil, nil); got != c.want {
				t.Errorf("sameFileSequence(nil, nil) = %v, want %v", got, c.want)
			}
			continue
		}
		if got := sameFileSequence(a, c.b); got != c.want {
			t.Errorf("%s: sameFileSequence(%v, %v) = %v, want %v", c.name, a, c.b, got, c.want)
		}
	}
}
