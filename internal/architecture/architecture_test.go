package architecture_test

import (
	"errors"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
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
		modulePath + "/internal/platform",
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
		modulePath + "/internal/platform/httperr",
		modulePath + "/internal/platform/httpx",
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

	for _, dir := range []string{"internal/modules", "internal/platform"} {
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

// internal/platform holds cross-cutting infrastructure and nothing else:
// configuration, what an error looks like on the wire, how a body gets onto it.
//
// This is the rule that stops a shared package becoming a dumping ground, and
// the admission test is not "more than one thing uses it" — that is a fact
// about the call graph, not about the concept. It is "does this decide
// something no business module owns". A package here that reaches a module has
// failed it, and is not platform code: it belongs in the module whose concept
// it is.
func TestPlatformKnowsNoBusiness(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range packagesUnder(t, root, "internal/platform") {
		for imported := range transitiveImports(t, root, pkg) {
			if matches(imported, modulePath+"/internal/modules") {
				t.Errorf("%s reaches %s\n\n"+
					"internal/platform holds cross-cutting infrastructure and nothing\n"+
					"else. If this needs to know what a ticket is, it is not platform\n"+
					"code — move it into the module whose concept it is.",
					shortName(pkg), shortName(imported))
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

// A feature never reaches another feature.
//
// This is the vertical slice invariant, and it is the one rule the refactor
// that created features/ actually needs. Without it the slices grow into each
// other and the filesystem stops answering the question it was reorganised to
// answer: a use case you cannot read without opening its neighbours is a use
// case in a layer again, whatever the directory is called.
//
// Transitive, like the module rule above and for the same reason. A feature
// that reaches another one through a shared helper has broken the boundary
// exactly as thoroughly as a direct import, and that is the shape these
// violations take — nobody writes the obvious one.
//
// What a feature *may* import is its own module's domain, its ports, and its
// transport package for the wire vocabulary more than one of them returns. The
// arrow points one way there and is load-bearing: transport takes built
// handlers rather than constructing them, precisely so it never has to name a
// feature and close a cycle.
//
// The escape hatch, when two features genuinely need the same thing, is to move
// that thing below both of them — into the domain if it is a rule, into ports
// if it is a contract, into transport if it is a wire shape. Not to import
// sideways.
func TestNoFeatureReachesAnotherFeature(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range packagesUnder(t, root, "internal/modules") {
		owner := featureOf(pkg)
		if owner == "" {
			continue
		}

		for imported := range transitiveImports(t, root, pkg) {
			other := featureOf(imported)
			if other == "" || other == owner {
				continue
			}
			t.Errorf("%s reaches %s\n\n"+
				"A feature is a use case, and a use case that needs its neighbour is\n"+
				"not one. Whatever they share belongs below them both: a rule in the\n"+
				"module's domain, a contract in its ports, a wire shape in its\n"+
				"transport package.",
				shortName(pkg), shortName(imported))
		}
	}
}

// A module's transport package holds no endpoint.
//
// Every use case owns its own HTTP adapter, under features/<use case>/, so that
// "where is creating a ticket" has one answer. This is what stops the old
// arrangement growing back one handler at a time — which is how it would grow
// back, because adding a handler next to the route table is always the smaller
// diff.
//
// The test is "returns an http.Handler" rather than anything about names,
// because that is what an endpoint *is* here and a name can be chosen to slip
// past. Middleware is exempt by construction rather than by exception: it
// returns func(http.Handler) http.Handler, which is a different type, and
// RequireAuth and RequireRole are not use cases — they decide whether a request
// may proceed, before any endpoint is chosen.
func TestTransportHoldsNoEndpoint(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range packagesUnder(t, root, "internal/modules") {
		if !strings.Contains(pkg, "/transport/") {
			continue
		}

		dir := filepath.Join(root, strings.TrimPrefix(pkg, modulePath))
		for _, fn := range functionsReturningHTTPHandler(t, dir) {
			t.Errorf("%s returns an http.Handler from %s\n\n"+
				"An endpoint belongs to the use case it serves, in\n"+
				"features/<use case>/. This package holds what the endpoints share —\n"+
				"the paths, the route table, the wire shapes, the middleware — and\n"+
				"nothing that answers a request itself.",
				fn, shortName(pkg))
		}
	}
}

// Every module organises its use cases under features/.
//
// The convention is worth more than the directory it costs a small module. An
// agent that has to decide *whether* a module gets features/ will decide
// wrongly, and "why is sla different" is a question with no good answer in a
// file the next person reads. sla has one feature and that is the rule working,
// not an exception to it.
//
// A module with no use cases at all would legitimately fail this, and none
// exists. If one is ever added, the honest fix is to ask what it is for.
func TestEveryModuleOrganisesItsUseCasesUnderFeatures(t *testing.T) {
	root := moduleRoot(t)

	entries, err := os.ReadDir(filepath.Join(root, "internal", "modules"))
	if err != nil {
		t.Fatalf("reading the modules directory: %v", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		features := filepath.Join(root, "internal", "modules", e.Name(), "features")
		if _, err := os.Stat(features); err != nil {
			t.Errorf("module %s has no features/ directory\n\n"+
				"Use cases live in features/<use case>/ in every module, however few\n"+
				"it has. One feature is not a reason to skip the directory; it is the\n"+
				"convention answering the question before anyone asks it.", e.Name())
		}
	}
}

// featureOf returns the package that owns importPath as a feature, or "" when
// importPath is not inside one.
//
// It returns the feature's own root rather than a bool, so a package and its
// (hypothetical) subpackages count as the same feature and comparing two
// results answers "different features?" directly.
func featureOf(importPath string) string {
	rest, ok := strings.CutPrefix(importPath, modulePath+"/internal/modules/")
	if !ok {
		return ""
	}

	module, rest, ok := strings.Cut(rest, "/features/")
	if !ok {
		return ""
	}

	feature, _, _ := strings.Cut(rest, "/")
	return module + "/features/" + feature
}

// functionsReturningHTTPHandler names every function in dir whose result list
// contains net/http's Handler.
//
// It reads the syntax rather than the type information, which is enough here
// and keeps the test free of a type checker: the result is written in the
// source as http.Handler, and a package that aliased net/http to something else
// to get around this would be doing so deliberately.
//
// The file list comes from build.ImportDir rather than parser.ParseDir — the
// latter is deprecated because it ignores build tags, and it is the same walker
// the rules above already use. Test files are excluded: a handler built inside
// a test is a fixture, not an endpoint somebody can reach.
func functionsReturningHTTPHandler(t *testing.T, dir string) []string {
	t.Helper()

	pkg, err := build.ImportDir(dir, 0)
	var noGo *build.NoGoError
	switch {
	case errors.As(err, &noGo):
		return nil
	case err != nil:
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()

	var found []string
	for _, name := range pkg.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				continue
			}
			for _, result := range fn.Type.Results.List {
				sel, ok := result.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "http" || sel.Sel.Name != "Handler" {
					continue
				}
				found = append(found, fn.Name.Name)
			}
		}
	}

	slices.Sort(found)
	return found
}
