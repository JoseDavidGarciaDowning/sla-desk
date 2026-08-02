package ticket_test

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file guards the structural rule in docs/spec.md §7 and the dependency
// direction recorded in docs/adr/0002. It lives here because internal/ticket is
// the package everything else is allowed to depend on and which may itself
// depend on nothing.
//
// Compilation catches a true import cycle on its own. It does not catch the
// softer failures this test exists for: internal/sla growing a pgx import
// "just for a type", or internal/ticket reaching for net/http to build an error
// response. Both compile. Both end the property that the domain is testable
// with nothing running.

const modulePath = "github.com/JoseDavidGarciaDowning/sla-desk"

// domainPackages must be pure: no database, no HTTP, no framework.
var domainPackages = []string{
	modulePath + "/internal/ticket",
	modulePath + "/internal/sla",
}

// forbidden are import paths a domain package must not reach, directly or
// through another domain package. Prefixes: forbidding pgx/v5 also forbids
// pgx/v5/pgtype.
var forbidden = []string{
	modulePath + "/internal/store",
	modulePath + "/internal/api",
	modulePath + "/internal/auth",
	modulePath + "/internal/config",
	"database/sql",
	"github.com/jackc/pgx",
	"github.com/go-chi/chi",
	"net/http",
}

func TestDomainPackagesDependOnNothingImpure(t *testing.T) {
	root := moduleRoot(t)

	for _, pkg := range domainPackages {
		t.Run(shortName(pkg), func(t *testing.T) {
			for imported := range transitiveImports(t, root, pkg) {
				for _, bad := range forbidden {
					if matches(imported, bad) {
						t.Errorf("%s reaches %s\n\n"+
							"The domain has no database and no HTTP (docs/spec.md §7). If this\n"+
							"is needed, the dependency belongs in the caller, not here.",
							shortName(pkg), imported)
					}
				}
			}
		})
	}
}

// The direction is one way and only one way: sla knows about tickets, tickets
// know nothing about SLAs. See docs/adr/0002 — internal/ticket owns transition
// legality, internal/sla owns deadline arithmetic, and the budget for a
// priority lives in the database rather than in either of them.
func TestTicketDoesNotImportSLA(t *testing.T) {
	root := moduleRoot(t)

	for imported := range transitiveImports(t, root, modulePath+"/internal/ticket") {
		if matches(imported, modulePath+"/internal/sla") {
			t.Errorf("internal/ticket reaches internal/sla\n\n" +
				"The dependency runs the other way. A ticket does not know what an SLA\n" +
				"is; the SLA clock reads a ticket's statuses.")
		}
	}
}

// matches reports whether importPath is prefix, or a package underneath it.
// A plain strings.HasPrefix would make "net/http" match a package called
// "net/httpsomething".
func matches(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

// transitiveImports walks the import graph, following packages inside this
// module and stopping at the boundary of external ones. An external package is
// recorded but not descended into: the rule is about what the domain reaches
// for itself, not about what its dependencies do internally.
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
			t.Fatalf("reading %s: %v", importPath, err)
		}
		for _, next := range p.Imports {
			walk(next)
		}
	}

	walk(pkg)
	delete(seen, pkg)
	return seen
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd() // the package directory: <root>/internal/ticket
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
