package difftour

import (
	"reflect"
	"testing"
)

func TestCuePlaceholders(t *testing.T) {
	cases := []struct {
		lines []int
		want  string
	}{
		{nil, ""},
		{[]int{3}, "[[line:+3]] "},
		{[]int{3, 4, 5}, "[[line:+3-5]] "},
		{[]int{0, 1, 7, 9, 10}, "[[line:+0-1]] [[line:+7]] [[line:+9-10]] "},
	}
	for _, tc := range cases {
		if got := cuePlaceholders(tc.lines); got != tc.want {
			t.Errorf("cuePlaceholders(%v) = %q, want %q", tc.lines, got, tc.want)
		}
	}
}

func TestSkeleton(t *testing.T) {
	ch := changes("X", "D", "Route", "B", "A")
	ch[3].AddedLines = []int{2, 3} // B
	ch[4].Status, ch[4].AddedLines = "added", nil
	r := Compute(fixture(), ch)
	sk := Skeleton(r, "main")

	if sk.Name != "diff-story" || sk.Title != "" || sk.Description != "" || sk.Generated {
		t.Errorf("skeleton header should be left to the author: %+v", sk)
	}
	if got := symbols(r.Functions); !reflect.DeepEqual(got, []string{"pkg.A", "pkg.B", "pkg.D", "pkg.Route", "pkg.X"}) {
		t.Fatalf("order = %v", got)
	}
	if len(sk.Stops) != 5 || sk.Stops[0].Symbol != "pkg.A" || sk.Stops[1].Symbol != "pkg.B" {
		t.Fatalf("stops = %+v", sk.Stops)
	}
	if sk.Stops[0].Description != "" {
		t.Errorf("an added function has no range to cue, got %q", sk.Stops[0].Description)
	}
	if sk.Stops[1].Description != "[[line:+2-3]] " {
		t.Errorf("B cues = %q", sk.Stops[1].Description)
	}
	if !reflect.DeepEqual(sk.Stops[3].Expand, []string{"pkg.B", "pkg.B > pkg.D"}) {
		t.Errorf("Route expand carried over: %v", sk.Stops[3].Expand)
	}
}
