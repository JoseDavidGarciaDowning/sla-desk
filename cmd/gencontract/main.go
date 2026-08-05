// Command gencontract writes the ticket contract out as a TypeScript module.
//
// It exists so the frontend's category list, priority list and field bounds
// come from the same declarations the API validates against, instead of being
// transcribed and then quietly drifting. Run it with `make contract`; a test in
// internal/api fails while the generated file is out of date, so forgetting is
// caught rather than shipped.
package main

import (
	"flag"
	"fmt"
	"os"

	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// defaultOut is relative to the repository root, which is where make runs.
const defaultOut = "web/lib/contract.ts"

func main() {
	out := flag.String("out", defaultOut, "path of the TypeScript module to write")
	flag.Parse()

	// 0o644, and the file is rewritten whole rather than appended to: the
	// generator's output is the entire content, and a partial write would leave
	// TypeScript that does not parse.
	if err := os.WriteFile(*out, []byte(tickethttp.TicketContract().TypeScript()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gencontract: writing %s: %v\n", *out, err)
		os.Exit(1)
	}

	fmt.Printf("wrote %s\n", *out)
}
