// Package architecture holds no code. It exists so the architecture rules have
// somewhere to live that belongs to no module — a test that enforces boundaries
// between modules cannot sit inside one of them without becoming a reason for
// that module to import the others.
//
// Everything is in architecture_test.go. See .golangci.yml for the fast half of
// the same rules.
package architecture
