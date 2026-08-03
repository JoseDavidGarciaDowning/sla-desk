"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";

import { PRIORITIES, STATUSES } from "@/lib/contract";

/**
 * Status and priority filters, held in the URL rather than in component state.
 *
 * docs/spec.md §8 requires this, and the reason is that a filtered view has to
 * be a thing you can send someone. State in a component cannot be linked to,
 * cannot be bookmarked, and does not survive a reload — a customer who filters
 * to their open tickets and refreshes should not land back on everything.
 *
 * The options come from the generated contract, so they are the values the API
 * accepts and cannot drift from them. A filter the API refuses is answered with
 * a 400 naming the field, which is the same document a rejected create returns.
 */
export function TicketFilters() {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  function apply(name: string, value: string) {
    const next = new URLSearchParams(params);

    if (value) {
      next.set(name, value);
    } else {
      // Deleted rather than set to "", so clearing a filter leaves the URL as
      // it was before anyone touched it. `?status=` and no parameter mean the
      // same thing to the API, but only one of them is a link worth sharing.
      next.delete(name);
    }

    // The cursor belongs to the unfiltered sequence. Carrying it across a
    // filter change would resume from a row that the new filter may exclude,
    // and the first page would silently start in the middle.
    next.delete("cursor");

    const query = next.toString();

    // replace, not push: every keystroke of filtering would otherwise become a
    // history entry, and the back button would walk the customer through each
    // one instead of returning them where they came from.
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  }

  return (
    <div className="flex flex-wrap items-end gap-4">
      <Filter
        name="status"
        label="Status"
        options={STATUSES}
        value={params.get("status") ?? ""}
        onChange={apply}
      />
      <Filter
        name="priority"
        label="Priority"
        options={PRIORITIES}
        value={params.get("priority") ?? ""}
        onChange={apply}
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
}: {
  name: string;
  label: string;
  options: readonly string[];
  value: string;
  onChange: (name: string, value: string) => void;
}) {
  return (
    <div className="space-y-1">
      <label htmlFor={`filter-${name}`} className="text-muted-foreground text-xs">
        {label}
      </label>
      <select
        id={`filter-${name}`}
        value={value}
        onChange={(event) => onChange(name, event.target.value)}
        className="h-8 rounded-lg border border-input bg-transparent px-2.5 py-1 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <option value="">Any</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option.charAt(0).toUpperCase() + option.slice(1)}
          </option>
        ))}
      </select>
    </div>
  );
}
