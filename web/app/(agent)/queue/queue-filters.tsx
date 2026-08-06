"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";

import { PRIORITIES, STATUSES } from "@/lib/contract";

/**
 * The queue's filters, in the URL for the reason docs/spec.md §8 gives: a
 * filtered view has to be something you can send someone. An agent who narrows
 * to unassigned urgent tickets and pastes that link to a colleague is sharing a
 * view rather than describing one.
 *
 * Status and priority come from the generated contract, so they are the values
 * the API accepts and cannot drift from them. Assignee does not, and cannot:
 * its vocabulary is this endpoint's own — "any", "unassigned", "me" — and there
 * is nothing in the shared contract to generate it from. It is transcribed
 * here, which is the thing the contract exists to avoid, so it is worth saying
 * why it is acceptable: an unknown value is answered with a 400 naming the
 * field, and the three words are the whole set. A wrong one fails loudly on the
 * first click rather than silently returning the wrong rows.
 */
const ASSIGNEE_OPTIONS = [
  { value: "me", label: "Mine" },
  { value: "unassigned", label: "Unassigned" },
] as const;

export function QueueFilters() {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  function apply(name: string, value: string) {
    const next = new URLSearchParams(params);

    if (value) {
      next.set(name, value);
    } else {
      // Deleted rather than set to "", so clearing a filter leaves the URL as
      // it was. Both mean the same to the API; only one is a link worth sending.
      next.delete(name);
    }

    // The cursor is a position in the sequence being paged — and on this
    // endpoint that sequence is in deadline order, which a filter change can
    // reshape entirely. Carrying it across would resume from a row the new
    // filter may exclude, and the first page would silently start in the middle.
    next.delete("cursor");

    const query = next.toString();

    // replace, not push: filtering would otherwise fill the history and the
    // back button would walk the agent through every keystroke.
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  }

  return (
    <div className="flex flex-wrap items-end gap-4">
      <Filter
        name="status"
        label="Status"
        options={STATUSES.map((s) => ({ value: s, label: s }))}
        value={params.get("status") ?? ""}
        onChange={apply}
      />
      <Filter
        name="priority"
        label="Priority"
        options={PRIORITIES.map((p) => ({ value: p, label: p }))}
        value={params.get("priority") ?? ""}
        onChange={apply}
      />
      <Filter
        name="assignee"
        label="Assignee"
        options={ASSIGNEE_OPTIONS}
        value={params.get("assignee") ?? ""}
        onChange={apply}
        allLabel="Anyone"
      />
    </div>
  );
}

function Filter({
  name,
  label,
  options,
  value,
  onChange,
  allLabel = "All",
}: {
  name: string;
  label: string;
  options: readonly { value: string; label: string }[];
  value: string;
  onChange: (name: string, value: string) => void;
  allLabel?: string;
}) {
  const id = `queue-filter-${name}`;

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-muted-foreground text-sm">
        {label}
      </label>
      <select
        id={id}
        name={name}
        value={value}
        onChange={(event) => onChange(name, event.target.value)}
        className="border-input bg-background h-9 rounded-md border px-3 text-sm"
      >
        <option value="">{allLabel}</option>
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </div>
  );
}
