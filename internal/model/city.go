package model

import "errors"

// The city is a spatial map of one repository: every package a district,
// every function a building. The server hands over the facts; the layout is
// the frontend's, because what the map has to be is *stable* — the same
// function in the same place across commits — and that is a property of the
// layout rule, which lives next to the drawing it governs.

// CityFunc is one building: a function with the measurements the map draws
// from and the counts a hover reports.
type CityFunc struct {
	ID   TargetID `json:"id"`
	Name string   `json:"name"`
	// File is the absolute path, as Frame.File reports it, so the frontend
	// can group a district's functions by file when there are too many to
	// draw one by one.
	File string `json:"file"`
	// Lines is the declaration's span, first line to last line inclusive.
	Lines int `json:"lines"`
	// Callers is how many distinct functions call this one, directly or
	// through an interface it implements. Callees is how many distinct
	// in-repo functions its body calls. Both are fan counts, not call-site
	// counts: a function called from one place ten times is still a leaf.
	Callers int `json:"callers"`
	Callees int `json:"callees"`
	// Changed is set only in diff mode (see --diff-base), for a function that
	// is new or whose body differs from the base. Omitted otherwise, so a
	// consumer can't mistake "diff mode off" for "nothing changed".
	Changed bool `json:"changed,omitempty"`
}

// CityPackage is one district.
type CityPackage struct {
	// Path is the import path; Name the package clause's name, which is what
	// code calls it and so what a reader will look for.
	Path  string     `json:"path"`
	Name  string     `json:"name"`
	Funcs []CityFunc `json:"funcs"`
}

// CityView is every package of one repository with its functions. Packages
// are sorted by path and functions by name, so two builds of the same code
// serialize identically.
type CityView struct {
	// Module is the module path, the common prefix a district's label drops.
	Module string `json:"module"`
	// Root is the repository directory, the common prefix a file label drops.
	Root     string        `json:"root"`
	Packages []CityPackage `json:"packages"`
}

// CityEngine is the optional half of Engine that can describe its project as
// a city. Kept separate from Engine for the same reason PlatformEngine is: an
// engine that can't answer stays a valid engine.
type CityEngine interface {
	// City returns the primary repository's packages and functions. The
	// result is memoized per index build and shared, so callers must not
	// mutate it.
	City() (*CityView, error)
}

// ErrNoCity is what a wrapping engine returns when the engine it holds can't
// list its functions (today, the TypeScript one).
var ErrNoCity = errors.New("city view is not available for this engine")

// CityOfEngine is implemented by engines that federate repositories, so the
// city of any workspace service can be drawn, not only the one unfold was
// pointed at. Ids in a non-primary repo's city carry the "<alias>::" prefix
// the rest of the API uses.
type CityOfEngine interface {
	CityOf(repo string) (*CityView, error)
}
