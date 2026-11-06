package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

// loadEffects indexes the effects fixture with the shipped rules plus any the
// test adds, the way a session with a repo rules file would.
func loadEffects(t *testing.T, extra ...rules.Rule) *Indexer {
	t.Helper()
	set := rules.Load()
	set.Rules = append(set.Rules, extra...)
	return loadEffectsSet(t, set)
}

func loadEffectsSet(t *testing.T, set rules.Set) *Indexer {
	t.Helper()
	dir, err := filepath.Abs("testdata/effects")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	idx := New()
	idx.SetRules(set)
	if err := idx.Load(dir, "./..."); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return idx
}

func effectsOf(t *testing.T, idx *Indexer, fn string) *model.EffectReport {
	t.Helper()
	rep, err := idx.Effects(TargetID("example.com/effects." + fn))
	if err != nil {
		t.Fatalf("Effects(%s): %v", fn, err)
	}
	return rep
}

// find returns the first effect of a kind, with the access asked for ("" for
// any), or nil.
func find(rep *model.EffectReport, kind, access string) *model.Effect {
	for n := range rep.Effects {
		e := &rep.Effects[n]
		if e.Kind == kind && (access == "" || e.Access == access) {
			return e
		}
	}
	return nil
}

// The direct cases, and the read/write distinction where the shape gives it:
// os.ReadFile is a read and os.WriteFile a write, Query a read and Exec a
// write, and the environment variable's name travels as the key.
func TestDirectEffectsDistinguishReadsFromWrites(t *testing.T) {
	idx := loadEffects(t)

	rc := effectsOf(t, idx, "readConfig")
	env := find(rc, "env", "read")
	if env == nil {
		t.Fatalf("readConfig: want an env read, got %+v", rc.Effects)
	}
	if env.Key != "APP_NAME" || env.Callee != "os.Getenv" || env.Depth != 0 {
		t.Errorf("env evidence = %+v, want key APP_NAME from os.Getenv at depth 0", *env)
	}
	if f := find(rc, "file", "read"); f == nil || f.Key != "/etc/app.conf" {
		t.Errorf("readConfig: want a file read of /etc/app.conf, got %+v", rc.Effects)
	}
	if f := find(rc, "file", "write"); f != nil {
		t.Errorf("readConfig writes nothing, but reported %+v", *f)
	}

	ws := effectsOf(t, idx, "writeState")
	if f := find(ws, "file", "write"); f == nil || f.Callee != "os.WriteFile" {
		t.Errorf("writeState: want a file write via os.WriteFile, got %+v", ws.Effects)
	}
	if g := find(ws, "global", "write"); g == nil || g.Callee != "main.counter" {
		t.Errorf("writeState: want a write to main.counter, got %+v", ws.Effects)
	}

	if q := effectsOf(t, idx, "query"); find(q, "db", "read") == nil || find(q, "db", "write") != nil {
		t.Errorf("query: want exactly a db read, got %+v", q.Effects)
	}
	if m := effectsOf(t, idx, "mutate"); find(m, "db", "write") == nil || find(m, "db", "read") != nil {
		t.Errorf("mutate: want exactly a db write, got %+v", m.Effects)
	}
}

// Statement shapes have no call site for a rule to match and are found in the
// AST: the goroutine, the send, and — through a rule — the sleep.
func TestStatementEffects(t *testing.T) {
	idx := loadEffects(t)
	rep := effectsOf(t, idx, "launch")
	if g := find(rep, "goroutine", ""); g == nil || g.Callee != "go query" {
		t.Errorf("launch: want a goroutine 'go query', got %+v", rep.Effects)
	}
	if c := find(rep, "channel", "write"); c == nil || c.Callee != "ch <-" {
		t.Errorf("launch: want a channel send on ch, got %+v", rep.Effects)
	}
	if s := find(rep, "time", ""); s == nil || s.Callee != "time.Sleep" {
		t.Errorf("launch: want time.Sleep, got %+v", rep.Effects)
	}
	// A goroutine launch is also a call: query's db read is inherited.
	if d := find(rep, "db", "read"); d == nil || d.Depth != 1 {
		t.Errorf("launch: want query's db read inherited at depth 1, got %+v", rep.Effects)
	}
}

func TestProcessAndNetworkEffects(t *testing.T) {
	idx := loadEffects(t)
	rep := effectsOf(t, idx, "shell")
	// Both the construction and the run are process effects; the program
	// name rides on the construction.
	var cmd, run bool
	for _, e := range rep.Effects {
		if e.Kind != "process" {
			continue
		}
		cmd = cmd || (e.Callee == "exec.Command" && e.Key == "ls")
		run = run || e.Callee == "exec.Cmd.Run"
	}
	if !cmd || !run {
		t.Errorf("shell: want exec.Command (key ls) and Cmd.Run as process effects, got %+v", rep.Effects)
	}
	if n := find(rep, "net", ""); n == nil || n.Key != "http://example.com" {
		t.Errorf("shell: want http.Get with its URL, got %+v", rep.Effects)
	}
	// http.Get is also an outbound binding; the two must not double up.
	nets := 0
	for _, e := range rep.Effects {
		if e.Kind == "net" {
			nets++
		}
	}
	if nets != 1 {
		t.Errorf("shell: http.Get reported %d times, want once", nets)
	}
}

// An inherited effect names the hop: which of the function's own callees it
// came through, and the call site that reaches it, so the badge can open the
// callee. Nothing is inherited from a callee with no effects.
func TestInheritedEffectsNameTheHop(t *testing.T) {
	idx := loadEffects(t)
	rep := effectsOf(t, idx, "handler")
	if rep.Truncated {
		t.Error("handler's walk is tiny and must not be truncated")
	}
	for _, e := range rep.Effects {
		if e.Depth != 1 {
			t.Errorf("handler has no direct effects, got %+v at depth %d", e, e.Depth)
		}
		if e.Via == "" || e.ViaCall == "" || e.ViaTitle == "" {
			t.Errorf("inherited effect must name its hop, got %+v", e)
		}
	}
	env := find(rep, "env", "read")
	if env == nil || env.Via != "example.com/effects.readConfig" || env.Site != env.Via {
		t.Errorf("env read should arrive via readConfig, got %+v", env)
	}
	if g := find(rep, "global", "write"); g == nil || g.Via != "example.com/effects.writeState" {
		t.Errorf("global write should arrive via writeState, got %+v", g)
	}
	if pure := effectsOf(t, idx, "pure"); len(pure.Effects) != 0 {
		t.Errorf("pure touches nothing, got %+v", pure.Effects)
	}
}

// The depth cap: an effect five calls down is not attributed, and the report
// says the walk stopped short; one call closer, it is, at depth 4.
func TestEffectDepthCap(t *testing.T) {
	idx := loadEffects(t)
	l0 := effectsOf(t, idx, "l0")
	if f := find(l0, "file", ""); f != nil {
		t.Errorf("l0: os.Remove is %d calls away and must not be attributed, got %+v", 5, *f)
	}
	if !l0.Truncated {
		t.Error("l0: the walk stopped at the depth cap with a callee unvisited, and must say so")
	}
	l1 := effectsOf(t, idx, "l1")
	f := find(l1, "file", "write")
	if f == nil || f.Depth != effectDepth {
		t.Fatalf("l1: want os.Remove at depth %d, got %+v", effectDepth, l1.Effects)
	}
	if f.Via != "example.com/effects.l2" {
		t.Errorf("l1: the hop is its own callee l2, got %q", f.Via)
	}
}

// A user rule with the effect role adds a badge: the in-house client is a
// database because the repo's rules file says so, and the evidence names the
// rule that decided it.
func TestUserEffectRuleAddsABadge(t *testing.T) {
	f, problems, err := rules.Parse([]byte(`{"rules":[
	  {"id":"test.cache",
	   "match":{"package":"example.com/effects","recv":"cache","func":"Put"},
	   "emit":{"role":"effect","kind":"db","access":"write","key":"{arg0}"}}
	]}`))
	if err != nil || len(problems) > 0 {
		t.Fatalf("parse: %v %v", err, problems)
	}
	idx := loadEffects(t, f.Rules...)
	rep := effectsOf(t, idx, "useCache")
	d := find(rep, "db", "write")
	if d == nil || d.Rule != "test.cache" || d.Key != "k" || d.Callee != "effects.cache.Put" {
		t.Fatalf("useCache: want a db write from rule test.cache with key k, got %+v", rep.Effects)
	}
	// The rule counts like any other, and the shipped ones read as built-in.
	var mine, builtin *model.RuleInfo
	for n, r := range idx.RuleReport().Rules {
		switch {
		case r.ID == "test.cache":
			mine = &idx.RuleReport().Rules[n]
		case r.ID == "effect.os.getenv":
			builtin = &idx.RuleReport().Rules[n]
		}
	}
	if mine == nil || mine.Matches != 1 {
		t.Errorf("test.cache should report one match, got %+v", mine)
	}
	if builtin == nil || !builtin.Builtin || builtin.Matches != 1 {
		t.Errorf("effect.os.getenv should be built-in with one match, got %+v", builtin)
	}
	// Switching the shipped rule off removes the badge; the mechanism is the
	// same settings-only entry a Go built-in takes.
	repoRules := filepath.Join(t.TempDir(), "recognizers.json")
	if err := os.WriteFile(repoRules, []byte(`{"rules":[{"id":"effect.os.getenv","enabled":false}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	idx2 := loadEffectsSet(t, rules.Load(repoRules))
	if e := find(effectsOf(t, idx2, "readConfig"), "env", ""); e != nil {
		t.Errorf("with effect.os.getenv disabled, readConfig still reports %+v", *e)
	}
}

// The dot on a folded call: a direct call into a callee with effects carries
// it, a call into a pure one doesn't, and a call into the stdlib never does.
func TestCallSiteHasEffects(t *testing.T) {
	idx := loadEffects(t)
	fr, err := idx.Frame("example.com/effects.handler")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range fr.Calls {
		got[c.DisplayName] = c.HasEffects
	}
	if !got["readConfig"] || !got["writeState"] {
		t.Errorf("readConfig and writeState have effects; got %v", got)
	}
	if got["pure"] {
		t.Errorf("pure has none; got %v", got)
	}
	fr, err = idx.Frame("example.com/effects.readConfig")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fr.Calls {
		if c.HasEffects {
			t.Errorf("stdlib call %s must not carry the dot — its callee is the effect, not a function with one", c.DisplayName)
		}
	}
}

// Effects are cached per index, and the copy handed out is the caller's to
// mutate.
func TestEffectsReportIsACopy(t *testing.T) {
	idx := loadEffects(t)
	a := effectsOf(t, idx, "readConfig")
	a.Effects[0].Kind = "mangled"
	a.ID = "elsewhere"
	b := effectsOf(t, idx, "readConfig")
	if b.ID != "example.com/effects.readConfig" || b.Effects[0].Kind == "mangled" {
		t.Errorf("a served report leaked a caller's mutation: %+v", b)
	}
}
