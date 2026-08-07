"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import {
  type AgentTicket,
  type NewTransition,
  agentKeys,
  allowedTransitions,
} from "@/lib/agent";
import { ApiError } from "@/lib/api";
import type { ActorRole, TicketStatus } from "@/lib/contract";

import { useApiFetch } from "@/lib/use-api";

/**
 * The moves an agent may make on a ticket, from where it is now.
 *
 * The buttons come from the generated transition table rather than from a list
 * written here, which is the whole reason that table is generated: the day an
 * edge changes in docs/spec.md §4.1, a hand-written list leaves a button on
 * screen that the API has started refusing.
 *
 * A terminal status renders nothing rather than a disabled row. There is
 * nothing to do to a closed ticket, and a greyed-out button invites a click
 * that will never work.
 *
 * The reason is optional and short. It is not a comment — comments are slice 4
 * — it is the note that explains the move in the timeline.
 */
export function TransitionControl({
  ticket,
  role,
}: {
  ticket: AgentTicket;
  role: ActorRole;
}) {
  const apiFetch = useApiFetch();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");

  const targets = allowedTransitions(ticket.status as TicketStatus, role);

  const move = useMutation({
    mutationFn: (body: NewTransition) =>
      apiFetch<AgentTicket>(`/api/agent/tickets/${ticket.id}/transitions`, {
        method: "POST",
        body: JSON.stringify(body),
      }),

    onSuccess: (updated) => {
      // The response is the real ticket, deadline included, so it is written
      // straight into the detail cache. Invalidating instead would blank the
      // view and refetch what the server just sent.
      queryClient.setQueryData(agentKeys.detail(updated.id), updated);

      // The queue is ordered by deadline, and a pause clears one — the row has
      // moved, so every cached page of it is stale. queues() is the prefix all
      // the filter combinations share.
      void queryClient.invalidateQueries({ queryKey: agentKeys.queues() });

      // The timeline gained a row. This one is invalidated rather than written,
      // because the API did not send the new entry back.
      void queryClient.invalidateQueries({
        queryKey: agentKeys.history(updated.id),
      });

      setReason("");
    },
  });

  if (targets.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        This ticket is closed. Nothing moves it from here.
      </p>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-1">
        <label htmlFor="transition-reason" className="text-muted-foreground text-sm">
          Reason (optional)
        </label>
        <input
          id="transition-reason"
          value={reason}
          maxLength={500}
          onChange={(event) => setReason(event.target.value)}
          placeholder="Waiting on the customer"
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
        />
      </div>

      <div className="flex flex-wrap gap-2">
        {targets.map((target) => (
          <Button
            key={target}
            variant="outline"
            // Disabled while a move is in flight, so a double click cannot send
            // two transitions — the second would be refused by the state
            // machine, but only after it had been sent.
            disabled={move.isPending}
            onClick={() =>
              move.mutate({
                to: target,
                // An empty box is no reason at all. The API stores whitespace
                // as absent anyway; not sending it keeps the two agreeing.
                reason: reason.trim() || undefined,
              })
            }
          >
            Move to {target}
          </Button>
        ))}
      </div>

      {move.error && (
        <p className="text-destructive text-sm" role="alert">
          {/* The API's own sentence, verbatim. A 403 here means the move exists
              and is not this role's to make, and that is worth reading rather
              than translating into "something went wrong". */}
          {move.error instanceof ApiError
            ? // fieldErrors.to first: a rejected move carries its reason there,
              // and it names the field the agent chose. message is the
              // problem document's detail when there was one to read.
              (move.error.fieldErrors.to ?? move.error.message)
            : "The API could not be reached."}
        </p>
      )}
    </div>
  );
}
