import { describe, expect, it } from "vitest";

import { allowedTransitions, isStaff, queueQuery } from "@/lib/agent";

describe("allowedTransitions", () => {
  // The control offers what the state machine allows and nothing else. These
  // expectations are the §4.1 edge table, and they are worth writing out
  // rather than derived from the same constant the code reads — otherwise the
  // test agrees with whatever the contract happens to say.
  it("offers an agent the moves that leave each status", () => {
    expect(allowedTransitions("open", "agent").sort()).toEqual([
      "pending",
      "resolved",
    ]);
    expect(allowedTransitions("pending", "agent").sort()).toEqual([
      "open",
      "resolved",
    ]);
    expect(allowedTransitions("resolved", "agent").sort()).toEqual([
      "closed",
      "open",
    ]);
  });

  it("offers a customer only the edges that are theirs", () => {
    // A customer reaches pending -> open by replying rather than by asking,
    // but the edge is theirs to take.
    expect(allowedTransitions("pending", "customer")).toEqual(["open"]);
    expect(allowedTransitions("resolved", "customer")).toEqual(["open"]);
    // Setting pending and resolving are an agent's moves.
    expect(allowedTransitions("open", "customer")).toEqual([]);
  });

  it("offers nothing at all from closed, for anyone", () => {
    // Terminal. The API needs no special case for it and neither does this.
    for (const role of ["customer", "agent", "admin"] as const) {
      expect(allowedTransitions("closed", role)).toEqual([]);
    }
  });

  it("gives an admin everything an agent has", () => {
    for (const from of ["open", "pending", "resolved"] as const) {
      const agent = allowedTransitions(from, "agent").sort();
      const admin = allowedTransitions(from, "admin").sort();
      expect(admin).toEqual(agent);
    }
  });
});

describe("isStaff", () => {
  it("admits agents and admins and nobody else", () => {
    expect(isStaff("agent")).toBe(true);
    expect(isStaff("admin")).toBe(true);
    expect(isStaff("customer")).toBe(false);
    // An unknown role and a missing one both fail closed. The layout redirects
    // on false, so anything it cannot recognise must not be let through.
    expect(isStaff("superadmin")).toBe(false);
    expect(isStaff(undefined)).toBe(false);
  });
});

describe("queueQuery", () => {
  it("drops empty filters rather than sending them", () => {
    expect(queueQuery({ status: "open", priority: "", assignee: "" })).toBe(
      "?status=open",
    );
    expect(queueQuery({})).toBe("");
  });
});
