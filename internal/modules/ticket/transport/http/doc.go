// Package http is the ticket module's HTTP surface as a whole: the paths, the
// route table, the wire shapes more than one feature returns, and the contract
// published to the frontend.
//
// What is *not* here is any endpoint. Each use case owns its own handler, its
// own request and response types and its own HTTP adapter, under
// features/<use case>/ — so "where is creating a ticket" has one answer and it
// is not this package. An architecture test enforces that by refusing any
// function here that returns an http.Handler.
//
// The direction is one-way and load-bearing: a feature imports this package for
// the vocabulary it shares with its neighbours, and this package imports no
// feature. Routes takes handlers that are already built rather than building
// them, which is what keeps that true — the module's front door constructs the
// features and hands them over.
package http
