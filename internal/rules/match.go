package rules

import (
	"strings"

	"github.com/MaxInertia/unfold/internal/platform"
)

// Captures are the named fragments a package pattern pulled out, e.g.
// {"domain": "orders"} from "foo/$(domain)/sdks/go". They're available to the
// key template, mostly as labels.
type Captures map[string]string

// matchPattern tests a "/"-segmented pattern against a path, filling captures.
//
// Segment-wise rather than character-wise, because these are import paths and
// the meaningful unit is the segment: "foo/*/sdks" should match
// "foo/orders/sdks" and not "foo/orders/extra/sdks". A "**" segment matches
// any number of segments, for the cases where depth genuinely varies.
//
// Deliberately not regular expressions. A path pattern is what people want to
// write and read here, and a regex in a shared config file is a thing the next
// person has to decode before they can trust the surface it produces.
func matchPattern(pattern, path string, caps Captures) bool {
	if pattern == "" {
		return true
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"), caps)
}

func matchSegments(pat, seg []string, caps Captures) bool {
	for len(pat) > 0 {
		p := pat[0]
		if p == "**" {
			// Try every split point; the shortest match wins, which keeps a
			// trailing literal ("**/sdks/go") anchored where you'd expect.
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:], caps) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if name, ok := captureName(p); ok {
			if seg[0] == "" {
				return false
			}
			if caps != nil {
				caps[name] = seg[0]
			}
		} else if !matchSegment(p, seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

// captureName recognizes "$(name)".
func captureName(s string) (string, bool) {
	if strings.HasPrefix(s, "$(") && strings.HasSuffix(s, ")") && len(s) > 3 {
		return s[2 : len(s)-1], true
	}
	return "", false
}

// matchSegment handles "*" wildcards inside one segment ("*Client", "New*").
func matchSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(s, last) && len(s) >= len(last)
}

// Matches reports whether a rule's direct predicate holds for a call site,
// filling in any captures. The CalleeMatches clause is *not* evaluated here —
// it needs the callee's body, which is the evaluator's job.
func (m *Match) Matches(c platform.Call, caps Captures) bool {
	if !matchPattern(m.Package, c.PkgPath, caps) {
		return false
	}
	if m.RecvOnly && c.Recv == "" {
		return false
	}
	if m.Recv != "" && !matchSegment(m.Recv, c.Recv) {
		return false
	}
	if m.RecvPkg != "" && !matchPattern(m.RecvPkg, c.RecvPkg, caps) {
		return false
	}
	if m.Func != "" && !matchSegment(m.Func, c.Func) {
		return false
	}
	if m.MinArgs > 0 && len(c.Args) < m.MinArgs {
		return false
	}
	for _, a := range m.Args {
		// An argument that isn't there can't have the type you asked for. This
		// is a rejection, not a skip: a rule constrained to "argument 2 is an
		// Event" must not fire on a two-argument call that never mentions one.
		if a.Index < 0 || a.Index >= len(c.Args) {
			return false
		}
		got := c.Args[a.Index]
		if a.Type != "" && !matchType(a.Type, got.Type) {
			return false
		}
		if a.ParamType != "" && !matchType(a.ParamType, got.ParamType) {
			return false
		}
	}
	return true
}

// matchType compares a type pattern to a rendered type.
//
// Segment-wise on "/" like an import path, because that is what most of the
// string is: "*github.com/acme/**/pb.Event" should behave the way the same
// pattern does against a package path. The leading "*" of a pointer type is
// part of the first segment and matches literally, so a rule that means the
// pointer says so — a rule accidentally written against the value type
// wouldn't fire, which is visible, where silently accepting both would put
// edges in the surface nobody asked for.
func matchType(pattern, got string) bool {
	if pattern == "" {
		return true
	}
	if got == "" {
		return false // nothing known about this argument; don't guess
	}
	return matchPattern(pattern, got, nil)
}

// Key expands a key template against a call site.
//
// "{arg0}" is the constant-folded value of argument 0, "{inner.key}" the key
// the nested rule produced, and "{name}" a capture. An unresolvable reference
// yields ok=false and the caller drops the binding rather than emitting one
// with a hole in it — a key built from a runtime value is exactly the case
// unfold declines to guess at.
func expandKey(tmpl string, c platform.Call, caps Captures, inner string) (string, bool) {
	s, ok, _ := expandKeyDetail(tmpl, c, caps, inner)
	return s, ok
}

// expandKeyDetail also reports whether any value it consumed was inferred —
// read out of a variable's initializer rather than folded from a constant — so
// the binding can be badged for what it actually is.
func expandKeyDetail(tmpl string, c platform.Call, caps Captures, inner string) (string, bool, bool) {
	var inferred bool
	var b strings.Builder
	rest := tmpl
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			return b.String(), true, inferred
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			b.WriteString(rest)
			return b.String(), true, inferred
		}
		b.WriteString(rest[:i])
		ref := rest[i+1 : i+j]
		rest = rest[i+j+1:]

		switch {
		case ref == "inner.key":
			if inner == "" {
				return "", false, false
			}
			b.WriteString(inner)
		case strings.HasPrefix(ref, "arg"):
			n, ok := argIndex(ref)
			if !ok || n >= len(c.Args) || !c.Args[n].Known {
				return "", false, false
			}
			b.WriteString(c.Args[n].Value)
			inferred = inferred || c.Args[n].Inferred
		default:
			v, ok := caps[ref]
			if !ok {
				return "", false, false
			}
			b.WriteString(v)
		}
	}
}

// argIndex parses "arg0", "arg12".
func argIndex(ref string) (int, bool) {
	digits := strings.TrimPrefix(ref, "arg")
	if digits == "" {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
