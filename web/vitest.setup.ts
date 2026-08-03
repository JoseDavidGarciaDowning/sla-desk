import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library unmounts automatically only when Vitest runs with globals
// enabled, and this project runs without them. Left out, every rendered
// component would stay in the document for the whole file: queries would match
// leftovers from earlier tests, and a test could pass on the previous test's
// markup.
afterEach(cleanup);
