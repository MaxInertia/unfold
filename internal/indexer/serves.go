package indexer

import (
	"path/filepath"
	"strings"

	"github.com/MaxInertia/unfold/internal/model"
	"github.com/MaxInertia/unfold/internal/rules"
)

// The general declared tier: what a service states it serves, in a rules file
// rather than in one organisation's manifest.
//
// `microservice.yaml` + `--proto-root` remains the best answer where it
// applies — a proto is the contract both ends are generated from — but it
// applies to gRPC, to a platform with a shared proto repository, and to
// nothing else. A `serves` entry says the same thing for any kind of key:
// this service is the inbound end of these.
//
// It sits *behind* both other sources rather than in front of them. Anything
// the code shows is described by the code, which knows where the
// implementation is; a declaration fills in only what nothing here can read —
// a service in a language this index doesn't cover, an API fronted by a
// gateway, a transport nobody has written a recognizer for — and the surfaces
// of repositories a workspace hasn't opened yet, where it costs no index at
// all (see the workspace's declaration layer).
//
// Unlike the manifest tier it does *not* mark an uncorroborated key stale.
// protoPaths are generated from the same repo's build, so an RPC nothing
// implements is real drift; a `serves` entry is written precisely for surface
// this index cannot see, and badging all of it stale would be noise where the
// manifest's badge is a signal.

// declaredServes returns the bindings a repo's `serves` entries imply, minus
// anything already known from the manifest or from code.
func (i *Indexer) declaredServes() []model.Binding {
	entries := i.ruleSet.ServesFor(i.rootDir, i.serviceName, filepath.Base(i.rootDir))
	if len(entries) == 0 {
		return nil
	}
	var out []model.Binding
	seen := map[string]bool{}
	for _, e := range entries {
		for _, key := range e.Keys {
			if seen[e.Kind+"\x00"+key] || i.alreadyKnown(e.Kind, key) {
				continue
			}
			seen[e.Kind+"\x00"+key] = true
			b := model.Binding{
				Role:       model.RoleInbound,
				Kind:       e.Kind,
				Key:        key,
				Confidence: model.ConfDeclared,
				Visibility: model.VisPlatform,
				Detail:     "declared in " + i.relativeTo(e.From),
			}
			// A key that names one RPC can still be linked to the code that
			// serves it, which is what makes a declared row openable. A
			// pattern can't: it stands for a set nothing here enumerates.
			if e.Kind == "grpc.method" && !strings.HasSuffix(key, rules.Wildcard) {
				if svc, method, ok := strings.Cut(key, "/"); ok {
					target, candidates := i.implementationOfRPC(svc, method)
					b.Target = target
					if target == "" {
						// Several implementations and no way to tell which:
						// enumerated rather than dropped, the same answer the
						// manifest tier gives, and for the same reason —
						// dropping the link leaves the row unopenable.
						for _, c := range candidates {
							b.Candidates = append(b.Candidates, model.Candidate{
								TargetID: c,
								Label:    i.funcs[c].title,
							})
						}
					}
				}
			}
			out = append(out, b)
		}
	}
	return out
}

// alreadyKnown reports whether the surface already carries this key from a
// source that knows more about it: the manifest, or the code itself.
//
// A pattern is dropped as soon as *any* known key falls under it. The
// declaration exists to cover what nothing here can read, so a service whose
// registrations are readable has no use for the line that says it serves them
// — and showing both would put "notes.v1.NotesService/*" beside the four
// methods it stands for.
func (i *Indexer) alreadyKnown(kind, key string) bool {
	for _, b := range i.bindings {
		if b.Role != model.RoleInbound || b.Kind != kind {
			continue
		}
		if rules.KeyMatches(key, b.Key) || rules.KeyMatches(b.Key, key) {
			return true
		}
	}
	return false
}

// relativeTo renders a rules file path against the repo, so a row says
// ".unfold/recognizers.json" rather than a machine-specific absolute path.
func (i *Indexer) relativeTo(path string) string {
	if rel, err := filepath.Rel(i.rootDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
