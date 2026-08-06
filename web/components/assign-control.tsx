"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import {
  type AgentTicket,
  type AssignableUsers,
  type NewAssignee,
  agentKeys,
} from "@/lib/agent";
import { ApiError } from "@/lib/api";

import { useApiFetch } from "@/lib/use-api";

/**
 * Who is on this ticket, and how to change it.
 *
 * Three actions, and they are three because the API distinguishes them: take
 * it, hand it to a named colleague, or take everyone off. Unassigning sends an
 * explicit `null` — the API refuses a body with the field absent rather than
 * reading it as an unassignment, so that a client sending `{}` by accident
 * cannot quietly clear somebody's work.
 *
 * The roster is the one place in this app that reads another user's id. It
 * arrives from GET /api/agent/assignable, which returns agents and admins by
 * predicate and lives inside the group that already refuses everyone else.
 *
 * `docs/spec.md` §4.3 grants assignment to agent and admin alike, so the select
 * is offered to both. That is the current rule rather than a settled one — the
 * push model under discussion would restrict handing work to others while
 * leaving self-assignment open to everyone.
 */
export function AssignControl({
  ticket,
  me,
}: {
  ticket: AgentTicket;
  me: { id: string };
}) {
  const apiFetch = useApiFetch();
  const queryClient = useQueryClient();

  const roster = useQuery({
    queryKey: agentKeys.assignable(),
    queryFn: () => apiFetch<AssignableUsers>("/api/agent/assignable"),
    // The roster changes when somebody is granted a role, which is a
    // deploy-time event (T17). Refetching it per ticket view would be a request
    // per navigation for an answer that is the same all day.
    staleTime: 5 * 60 * 1000,
  });

  const assign = useMutation({
    mutationFn: (body: NewAssignee) =>
      apiFetch<AgentTicket>(`/api/agent/tickets/${ticket.id}/assignee`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),

    onSuccess: (updated) => {
      // The response is the updated ticket, so it is written rather than
      // refetched — the same reasoning the transition control uses.
      queryClient.setQueryData(agentKeys.detail(updated.id), updated);

      // The queue can be filtered by assignee, so a row may have just left or
      // joined the view being looked at. No history entry is written by an
      // assignment (plan §E), so the timeline is left alone.
      void queryClient.invalidateQueries({ queryKey: agentKeys.queues() });
    },
  });

  const assignedToMe = ticket.assignee_id === me.id;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {!assignedToMe && (
          <Button
            disabled={assign.isPending}
            onClick={() => assign.mutate({ assignee_id: me.id })}
          >
            Assign to me
          </Button>
        )}

        {ticket.assignee_id && (
          <Button
            variant="outline"
            disabled={assign.isPending}
            // Explicit null. The API refuses an absent field, which is what
            // stops an accidental empty body from clearing this.
            onClick={() => assign.mutate({ assignee_id: null })}
          >
            Unassign
          </Button>
        )}
      </div>

      <div className="flex flex-col gap-1">
        <label htmlFor="assignee" className="text-muted-foreground text-sm">
          Hand it to
        </label>
        <select
          id="assignee"
          value={ticket.assignee_id ?? ""}
          disabled={assign.isPending || roster.isPending}
          onChange={(event) =>
            assign.mutate({ assignee_id: event.target.value || null })
          }
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
        >
          <option value="">Nobody</option>
          {roster.data?.users.map((user) => (
            <option key={user.id} value={user.id}>
              {/* The role is shown because an agent and an admin are different
                  colleagues to hand a ticket to. The API sends a name that is
                  already the email when Clerk holds no name, so this never
                  renders a blank row. */}
              {user.name} ({user.role})
            </option>
          ))}
        </select>
      </div>

      {roster.error && (
        <p className="text-muted-foreground text-sm">
          {/* The roster failing does not stop the two buttons above from
              working, and saying so is more useful than an error banner over a
              control that still functions. */}
          The list of colleagues could not be loaded. You can still take this
          ticket or drop it.
        </p>
      )}

      {assign.error && (
        <p className="text-destructive text-sm" role="alert">
          {assign.error instanceof ApiError
            ? // The API names the field it objected to — "that user may not
              // hold tickets" is the sentence worth showing, not a status code.
              (assign.error.fieldErrors.assignee_id ?? assign.error.message)
            : "The API could not be reached."}
        </p>
      )}
    </div>
  );
}
