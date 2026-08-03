"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useApiFetch } from "@/lib/use-api";
import { type NewTicket, type Ticket, ticketKeys } from "@/lib/tickets";

/**
 * Creates a ticket and settles the cache afterwards.
 *
 * **There is no optimistic update here, and that is the decision, not an
 * omission.** Optimism pays when the client can predict the result and the user
 * stays where they are. Neither holds for creating a ticket: the server assigns
 * the id, the status, the timestamps and the SLA deadline, so an optimistic row
 * would be mostly invented — and the id it is missing is the one thing needed
 * to navigate to it.
 *
 * What replaces it costs less and lies about nothing. The response is the real
 * ticket, so it is written straight into the detail cache; the page it lands on
 * renders from that without a fetch. Optimism's benefit, without a rollback
 * path to maintain or a row that changes shape once the truth arrives.
 *
 * docs/spec.md §9 lists "optimistic update + rollback paths" under frontend
 * tests. That belongs to replies, in slice 2, where the client does know what
 * it is adding and the user does stay on the page.
 */
export function useCreateTicket() {
  const apiFetch = useApiFetch();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (ticket: NewTicket) =>
      apiFetch<Ticket>("/api/tickets", {
        method: "POST",
        body: JSON.stringify(ticket),
      }),

    onSuccess: (created) => {
      queryClient.setQueryData(ticketKeys.detail(created.id), created);

      // The list only, never ticketKeys.all: invalidating the whole subtree
      // would mark the detail seeded on the line above as stale, and it would
      // be refetched the moment the detail page mounted.
      //
      // Not awaited. The refetch is background work, and making the caller wait
      // for it would hold the user on the form while a list they are leaving
      // reloads.
      void queryClient.invalidateQueries({ queryKey: ticketKeys.list() });
    },

    // Navigation is deliberately not here. Where to go next is the calling
    // page's business, and this hook is about the cache; a call site passes its
    // own onSuccess to mutate() and it runs after this one.
  });
}
