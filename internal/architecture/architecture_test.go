package architecture_test

import (
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The slow half of the architecture rules. depguard (.golangci.yml) is the fast
// half: it reads one file's import block and catches a direct illegal import in
// seconds, on every lint run.
//
// What it cannot do is follow the graph. A ticket package that imports a
// harmless-looking helper which imports the SLA module passes depguard and
// breaks the boundary just as thoroughly — and that is the shape these
// violations actually take, because nobody writes the obvious one. Everything
// below walks transitively.
//
// It is also the check that survives suppression: a //nolint:depguard silences
// the linter and does nothing here.

const modulePath = "github.com/JoseDavidGarciaDowning/sla-desk"

// The business modules. Adding one here is the whole cost of adding a module:
// every rule below is expressed over this list rather than written out per
// module, so a new module is enforced from the moment it is named.
var modules = []string{"ticket", "sla", "identity"}

// layers is the dependency direction inside a module. Earlier entries may not
// be reached from later ones — transport may use application, application may
// not use transport.
var layers = []string{"domain", "application", "infrastructure", "transport"}

// TestModulesDoNotReachEachOther is the rule the whole structure exists for.
//
// Not "do not import" — do not *reach*. A module may not arrive at another one
// by any path, however many hops it takes, because a dependency that needs two
// hops to see is exactly the one that gets added by accident.
func TestModulesDoNotReachEachOther(t *testing.T) {
	root := moduleRoot(t)

	for _, from := range modules {
		for _, to := range modules {
			if from == to {
				continue
			}

			t.Run(from+"/->/"+to, func(t *testing.T) {
				target := modulePath + "/internal/modules/" + to

				for _, pkg := range packagesUnder(t, root, "internal/modules/"+from) {
					for imported := range transitiveImports(t, root, pkg) {
						if matches(imported, target) {
							t.Errorf("%s reaches %s\n\n"+
								"Modules do not import each other (docs/adr/0005). Declare what\n"+
								"you need as a contract in %s's own application or transport\n"+
								"layer, and wire the implementation in internal/app.",
								shortName(pkg), shortName(imported), from)
						}
					}
				}
			})
		}
	}
}

// A domain package is pure: no database, no HTTP, no framework, no SDK.
//
// Compilation catches a true import cycle on its own. It does not catch the
// softer failures this exists for: a domain package growing a pgx import "just
// for a type", or reaching for net/http to build an error response. Both
// compile. Both end the property that the domain is testable with nothing
// running.
func TestDomainPackagesArePure(t *testing.T) {
	root := moduleRoot(t)

	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/platform",
		"database/sql",
		"net/http",
		"github.com/jackc/pgx",
		"github.com/go-chi/chi",
		"github.com/clerk/clerk-sdk-go",
		"github.com/svix/svix-webhooks",
	}

	for _, m := range modules {
		t.Run(m, func(t *testing.T) {
			for _, pkg := range packagesUnder(t, root, "internal/modules/"+m+"/domain") {
				for imported := range transitiveImports(t, root, pkg) {
					for _, bad := range forbidden {
						if matches(imported, bad) {
							t.Errorf("%s reaches %s\n\n"+
								"The domain has no database, no HTTP and no framework\n"+
								"(docs/spec.md §7). If this is needed, the dependency belongs\n"+
								"in the layer above, not here.",
								shortName(pkg), imported)
						}
					}
				}
			}
		})
	}
}

// The layer direction, inside each module: transport -> application -> domain,
// with infrastructure implementing the application's contracts.
//
// Stated as "a lower layer never reaches a higher one", which is the form that
// stays true when a module grows a layer nobody has thought of yet.
func TestLayersOnlyDependDownwards(t *testing.T) {
	root := moduleRoot(t)

	for _, m := range modules {
		for i, lower := range layers {
			// Only forward pairs are checked, which is what makes
			// infrastructure -> application legal while application ->
			// infrastructure is not: infrastructure implements the contracts
			// the application declares, so the arrow runs that way and only
			// that way.
			for _, higher := range layers[i+1:] {
				t.Run(m+"/"+lower+"/->/"+higher, func(t *testing.T) {
					target := modulePath + "/internal/modules/" + m + "/" + higher

					for _, pkg := range packagesUnder(t, root, "internal/modules/"+m+"/"+lower) {
						for imported := range transitiveImports(t, root, pkg) {
							if matches(imported, target) {
								t.Errorf("%s reaches %s\n\n"+
									"The arrows run transport -> application -> domain, and\n"+
									"infrastructure implements the application's contracts.\n"+
									"This one runs backwards.",
									shortName(pkg), shortName(imported))
							}
						}
					}
				})
			}
		}
	}
}

// internal/platform is cross-cutting infrastructure: logging, configuration,
// HTTP mechanics, the database handle. It must not know what a ticket is.
//
// This is the rule that stops a shared package becoming a dumping ground. The
// admission test is not "more than one thing uses it" — that is a fact about
// the call graph, not about the concept — it is "does this decide something no
// business module owns". A package here that reaches a module has failed it.
func TestPlatformKnowsNoBusiness(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range packagesUnder(t, root, "internal/platform") {
		for imported := range transitiveImports(t, root, pkg) {
			if matches(imported, modulePath+"/internal/modules") ||
				matches(imported, modulePath+"/internal/app") {
				t.Errorf("%s reaches %s\n\n"+
					"internal/platform holds cross-cutting infrastructure and nothing\n"+
					"else. If this needs to know what a ticket is, it is not platform\n"+
					"code — move it into the module whose concept it is.",
					shortName(pkg), shortName(imported))
			}
		}
	}
}

// internal/app is a sink. Everything may be reached from it; nothing may reach
// it back except the commands that build it.
//
// Compilation already refuses the direct case, because app imports every
// module. What this catches is the indirect one — a package outside the module
// tree that app does not import, quietly reaching into the wiring.
func TestNothingReachesTheCompositionRoot(t *testing.T) {
	root := moduleRoot(t)

	for _, dir := range []string{"internal/modules", "internal/platform"} {
		for _, pkg := range packagesUnder(t, root, dir) {
			for imported := range transitiveImports(t, root, pkg) {
				if matches(imported, modulePath+"/internal/app") {
					t.Errorf("%s reaches internal/app\n\n"+
						"The composition root is the only place that may know two modules\n"+
						"exist, and only cmd may build it. A package needing something\n"+
						"from another module declares a contract instead.",
						shortName(pkg))
				}
			}
		}
	}
}

// internal/platform/httperr and httpx are the packages every layer may call.
// That only works while they sit below all of them, and they sit below them
// only while they import none of them.
//
// The rule is stricter than "no cycle today". Letting httperr import a domain
// package to name a role in a message, say, would put it above that module —
// and the next module that wanted to answer a request would find httperr
// already spoken for. httperr was extracted precisely because the auth
// middleware could not reach the API's problem writer without a cycle; a
// package that can drift back into the same position solves nothing.
func TestTheErrorAndHTTPHelpersDependOnNothingInThisModule(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range []string{
		modulePath + "/internal/platform/httperr",
		modulePath + "/internal/platform/httpx",
	} {
		t.Run(shortName(pkg), func(t *testing.T) {
			for imported := range transitiveImports(t, root, pkg) {
				if matches(imported, modulePath) {
					t.Errorf("%s reaches %s\n\n"+
						"It has to stay below every layer that reports an error, or the\n"+
						"layer it now sits above cannot use it. Whatever this import was\n"+
						"needed for belongs in the caller.", shortName(pkg), imported)
				}
			}
		})
	}
}

// Every module owns its own generated queries, and no module has a package that
// generates another module's tables.
//
// The real guarantee is in each sqlc.yaml — omit_unused_structs means a module
// only gets models for tables its own queries touch, so the ticket module has
// no User struct to reach for. This asserts the arrangement that guarantee
// depends on: one config per module, none shared.
func TestEachModuleOwnsItsOwnSQLCConfig(t *testing.T) {
	root := moduleRoot(t)

	if _, err := os.Stat(filepath.Join(root, "sqlc.yaml")); err == nil {
		t.Error("a root sqlc.yaml exists\n\n" +
			"Queries belong to the module that owns the data. One shared config\n" +
			"means one generated package every module can reach into.")
	}

	for _, m := range modules {
		cfg := filepath.Join(root, "internal", "modules", m, "infrastructure", "postgres", "sqlc.yaml")
		if _, err := os.Stat(cfg); err != nil {
			// Not every module has to own tables — but if it has a postgres
			// directory, it has to own its config.
			dir := filepath.Join(root, "internal", "modules", m, "infrastructure", "postgres")
			if _, dirErr := os.Stat(dir); dirErr == nil {
				t.Errorf("%s has infrastructure/postgres but no sqlc.yaml of its own", m)
			}
		}
	}
}

// matches reports whether importPath is prefix, or a package underneath it.
// A plain strings.HasPrefix would make "net/http" match a package called
// "net/httpsomething".
func matches(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

// packagesUnder lists every package in this module below dir, so a rule covers
// packages that do not exist yet. Written out per package instead of once per
// module, because that is what makes the failure name the offender.
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
// that does not hold — and it is the easiest place for one to be breached,
// because "it is only a test" is how every such import is justified.
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
		if err != nil {
			// A directory with no buildable Go files for this build
			// configuration — an integration-tagged package, say. Nothing to
			// walk, and not a failure.
			return
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
