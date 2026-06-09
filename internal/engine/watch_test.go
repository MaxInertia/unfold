package engine

import "testing"

func TestIsReindexTrigger(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Source files trigger by extension.
		{"/proj/main.go", true},
		{"/proj/src/app.ts", true},
		{"/proj/src/app.tsx", true},
		{"/proj/comp.html", true},
		{"/proj/mod.mts", true},

		// Only the manifests that affect module resolution trigger for .json.
		{"/proj/package.json", true},
		{"/proj/tsconfig.json", true},
		{"/proj/tsconfig.app.json", true},
		{"/proj/jsconfig.json", true},

		// Arbitrary .json data files do not.
		{"/proj/data/fixture.json", false},
		{"/proj/i18n/en.json", false},
		{"/proj/package-lock.json", false},

		// Non-source extensions are ignored.
		{"/proj/README.md", false},
		{"/proj/logo.png", false},
		{"/proj/notes.txt", false},
	}
	for _, tc := range cases {
		if got := isReindexTrigger(tc.path); got != tc.want {
			t.Errorf("isReindexTrigger(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
