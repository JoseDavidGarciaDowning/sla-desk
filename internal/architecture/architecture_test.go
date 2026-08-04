package architecture_test

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The boundary rules, as a test.
//
// This package exists before the modules do. The rules below describe where the
// backend is going, and each refactor step turns one of them from red to green —
// so a boundary is never asserted by a comment nobody runs.
//
// It walks the import graph *transitively*, which is the whole reason it is a
// test and not only a linter rule. A package that reaches another one through an
// innocent-looking helper has broken the boundary exactly as thoroughly as a
// direct import, and that is the shape these violations actually take, because
// nobody writes the obvious one.

const modulePath = "github.com/JoseDavidGarciaDowning/sla-desk"

// TestDomainsDoNotReachEachOther is the rule the whole refactor exists for.
//
// Not "do not import" — do not *reach*. internal/ticket was a shared kernel:
// auth and sla both pointed at it for vocabulary they should have owned
// themselves, and neither could move while they did. Each PR cuts one of those
// arrows and adds the rule that keeps it cut.
//
// The pairs are listed rather than generated because the structure is not
// symmetric yet: internal/ticket is still a bare package rather than a module,
// and internal/store legitimately sees everything — it is the composition point
// until internal/app replaces it in PR 5.
func TestDomainsDoNotReachEachOther(t *testing.T) {
	root := moduleRoot(t)

	forbidden := []struct{ from, to string }{
		// The SLA module borrowed ticket's Status and Priority. It works in
		// running and paused phases; which statuses burn budget is the ticket
		// module's decision, and it already asks the status itself.
		{"internal/modules/sla", "internal/modules/ticket"},
		{"internal/modules/sla", "internal/modules/identity"},

		// The identity module owns users, and nothing about a ticket or an SLA.
		//
		// This is the rule internal/auth could not carry. sqlc maps users.role
		// onto whichever Go type sqlc.yaml names, so pointing it at an
		// auth-owned type made the generated store import auth — which already
		// imported store, closing a cycle. The module generates its own queries
		// against its own domain, and the cycle has nowhere to form.
		{"internal/modules/identity", "internal/modules/ticket"},
		{"internal/modules/identity", "internal/modules/sla"},

		// And the last pair. internal/ticket was the shared kernel every other
		// package leaned on; it is now a module that leans on nobody.
		{"internal/modules/ticket", "internal/modules/sla"},
		{"internal/modules/ticket", "internal/modules/identity"},
	}

	for _, rule := range forbidden {
		t.Run(rule.from+"/->/"+rule.to, func(t *testing.T) {
			target := modulePath + "/" + rule.to

			for _, pkg := range packagesUnder(t, root, rule.from) {
				for imported := range transitiveImports(t, root, pkg) {
					if matches(imported, target) {
						t.Errorf("%s reaches %s\n\n"+
							"These are separate domains. What %s needs, it declares in its\n"+
							"own vocabulary — the strings may match, the meanings do not.",
							shortName(pkg), shortName(imported), rule.from)
					}
				}
			}
		})
	}
}

// A module's domain package is pure: no database, no HTTP, no framework, no SDK.
//
// Compilation catches a true import cycle on its own. It does not catch the
// softer failures this exists for: a domain package growing a pgx import "just
// for a type", or reaching for net/http to build an error response. Both
// compile. Both end the property that the domain is testable with nothing
// running.
//
// This rule is why identity's User carries a uuid.UUID rather than the
// pgtype.UUID internal/auth used. That field was the one thing standing between
// the domain and this test.
func TestModuleDomainsArePure(t *testing.T) {
	root := moduleRoot(t)

	forbidden := []string{
		"database/sql",
		"net/http",
		"github.com/jackc/pgx",
		"github.com/go-chi/chi",
		"github.com/clerk/clerk-sdk-go",
		"github.com/svix/svix-webhooks",

		// The packages above a domain. internal/ticket's own test forbade
		// these before it was absorbed into this one, and dropping them here
		// would have quietly weakened the rule while the test count still
		// looked fine.
		modulePath + "/internal/api",
		modulePath + "/internal/config",
	}

	for _, pkg := range packagesUnder(t, root, "internal/modules") {
		if !strings.Contains(pkg, "/domain") {
			continue
		}
		for imported := range transitiveImports(t, root, pkg) {
			for _, bad := range forbidden {
				if matches(imported, bad) {
					t.Errorf("%s reaches %s\n\n"+
						"A domain package has no database, no HTTP and no framework\n"+
						"(docs/spec.md §7). If this is needed, the dependency belongs in\n"+
						"the layer above, not here.",
						shortName(pkg), imported)
				}
			}
		}
	}
}

// internal/httperr is the one package every layer may call: the domain does
// not, but every module's transport and every adapter reports failures the
// same way through it.
// That only works while it sits below all of them, and it sits below them only
// while it imports none of them.
//
// The rule is stricter than "no cycle today". Letting it import a domain
// package to name a role in a message, say, would put it above that module —
// and the next
// module that wanted to answer a request would find httperr already spoken
// for. It was extracted precisely because the auth middleware could not reach
// the API's problem writer without a cycle; a package that can drift back into
// the same position solves nothing.
//
// internal/httpx joined it under the same rule when writeJSON turned out to be
// needed by two transports at once.
//
// Note this is the opposite constraint to the domain rule above: both are
// allowed net/http, which a domain is not. They are not domain packages. They
// are leaves that know one thing each — what an error looks like on the wire,
// and how a body gets onto it.
func TestTheHTTPLeavesDependOnNothingInThisModule(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range []string{
		modulePath + "/internal/httperr",
		modulePath + "/internal/httpx",
	} {
		t.Run(shortName(pkg), func(t *testing.T) {
			for imported := range transitiveImports(t, root, pkg) {
				if matches(imported, modulePath) {
					t.Errorf("%s reaches %s\n\n"+
						"It has to stay below every layer that answers a request, or the\n"+
						"layer it now sits above cannot use it. Whatever this import was\n"+
						"needed for belongs in the caller.", shortName(pkg), imported)
				}
			}
		})
	}
}

// Every module owns its own generated queries, and no config generates another
// module's tables.
//
// The real guarantee is in each sqlc.yaml: omit_unused_structs means a module
// only gets structs for tables its own queries touch, so the ticket module has
// no User to reach for even though tickets references users. This asserts the
// arrangement that guarantee depends on — one config per module, and none
// shared.
func TestEachModuleOwnsItsOwnSQLCConfig(t *testing.T) {
	root := moduleRoot(t)

	if _, err := os.Stat(filepath.Join(root, "sqlc.yaml")); err == nil {
		t.Error("a root sqlc.yaml exists\n\n" +
			"Queries belong to the module that owns the data. One shared config\n" +
			"means one generated package every module can reach into, which is\n" +
			"the arrangement this refactor removed.")
	}

	for _, m := range []string{"ticket", "sla", "identity"} {
		dir := filepath.Join(root, "internal", "modules", m, "infrastructure", "postgres")
		if _, err := os.Stat(dir); err != nil {
			continue // a module need not own tables
		}
		if _, err := os.Stat(filepath.Join(dir, "sqlc.yaml")); err != nil {
			t.Errorf("%s has infrastructure/postgres but no sqlc.yaml of its own", m)
		}
	}
}

// internal/app is a sink. Everything may be reached from it; nothing may reach
// it back except the commands that build it.
//
// Compilation already refuses the direct case, because app imports every
// module. What this catches is the indirect one — a package outside the module
// tree that app does not import, quietly reaching into the wiring. A module
// that reached the composition root would be depending on its own neighbours
// through the back door, which is the whole arrangement this refactor removed.
func TestNothingReachesTheCompositionRoot(t *testing.T) {
	root := moduleRoot(t)

	for _, dir := range []string{"internal/modules", "internal/httperr", "internal/httpx", "internal/config"} {
		for _, pkg := range packagesUnder(t, root, dir) {
			for imported := range transitiveImports(t, root, pkg) {
				if matches(imported, modulePath+"/internal/app") {
					t.Errorf("%s reaches internal/app\n\n"+
						"The composition root is the only place that may know two modules\n"+
						"exist, and only cmd may build it. A package needing something from\n"+
						"another module declares a contract instead.",
						shortName(pkg))
				}
			}
		}
	}
}

// matches reports whether importPath is prefix, or a package underneath it.
// A plain strings.HasPrefix would make "net/http" match "net/httpsomething".
func matches(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

// packagesUnder lists every package in this module below dir, so a rule covers
// packages that do not exist yet. Named per package rather than per domain,
// because that is what makes a failure name the offender.
func packagesUnder(t *testing.T, root, dir string) []string {
	t.Helper()

	var found []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p, err := build.ImportDir(path, 0); err == nil && len(p.GoFiles)+len(p.TestGoFiles) > 0 {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found = append(found, modulePath+"/"+filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}

	slices.Sort(found)
	return found
}

// transitiveImports walks the import graph, following packages inside this
// module and stopping at the boundary of external ones. An external package is
// recorded but not descended into: the rule is about what our code reaches for
// itself, not about what its dependencies do internally.
//
// Test files are included. A test that reaches across a boundary is a boundary
// that does not hold, and it is the easiest place for one to be breached,
// because "it is only a test" is how every such import gets justified.
func transitiveImports(t *testing.T, root, pkg string) map[string]bool {
	t.Helper()

	seen := make(map[string]bool)
	var walk func(string)
	walk = func(importPath string) {
		if seen[importPath] {
			return
		}
		seen[importPath] = true

		if !matches(importPath, modulePath) {
			return
		}

		dir := filepath.Join(root, strings.TrimPrefix(importPath, modulePath))
		p, err := build.ImportDir(dir, 0)

		// A package whose files are all behind a build tag this run does not
		// have is legitimately empty, and skipping it is correct. Any other
		// error means a package could not be read — and a walker that treats
		// "I could not look" the same as "there is nothing there" reports a
		// boundary as intact because it failed to check it.
		var noGo *build.NoGoError
		switch {
		case errors.As(err, &noGo):
			return
		case err != nil:
			t.Fatalf("reading %s: %v", importPath, err)
		}
		for _, next := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
			walk(next)
		}
	}

	walk(pkg)
	delete(seen, pkg)
	return seen
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd() // the package directory: <root>/internal/architecture
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..")

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected go.mod at %s: %v", root, err)
	}
	return root
}

func shortName(importPath string) string {
	return strings.TrimPrefix(importPath, modulePath+"/")
}
