"use client";

import { useRouter } from "next/navigation";
import { useRef } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError } from "@/lib/api";
import {
  CATEGORIES,
  MAX_DESCRIPTION_LENGTH,
  MAX_TITLE_LENGTH,
  PRIORITIES,
} from "@/lib/contract";
import type { NewTicket } from "@/lib/tickets";
import { useCreateTicket } from "@/lib/use-tickets";

/**
 * The order fields appear in, which is also the order they are inspected in
 * when deciding where to send focus after a rejection.
 */
const FIELDS = ["title", "description", "category", "priority"] as const;

/**
 * The priority a ticket gets when the customer expresses no opinion.
 *
 * Category has no equivalent — a default there would make one category the
 * bin everything lands in, and the agent dashboard filters by it. Priority
 * does: "normal" is the middle of the four and the one most tickets are.
 */
const DEFAULT_PRIORITY = "normal";

/** Turns a select value into a Title Case label without a second list to keep. */
function humanise(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

/**
 * Says what went wrong, when the server did not attribute it to a field.
 *
 * A 401 gets its own sentence rather than the status. "The API answered 401" is
 * accurate and useless: it reads as a fault in the system, so the user retries,
 * and retrying is the one thing that cannot work.
 *
 * It also does not reload the page, which is what a failed *query* does — see
 * the QueryCache handler in app/providers.tsx. The asymmetry is deliberate: a
 * query has nothing unsaved to lose, and a form has a draft the user has just
 * spent time on. Sending them through Clerk would throw it away to fix a
 * problem they can fix in another tab.
 */
function describeFailure(error: Error): string {
  if (!(error instanceof ApiError)) {
    return "The ticket could not be created — the API could not be reached.";
  }

  if (error.status === 401) {
    return (
      "Your session has ended. Sign in again in another tab, then submit — " +
      "nothing you have typed here is lost."
    );
  }

  return `The ticket could not be created — the API answered ${error.status}.`;
}

/**
 * The create-ticket form.
 *
 * **It validates almost nothing, deliberately.** `required` and `maxlength` are
 * the browser's own, and their limits come from the generated contract; the
 * selects can only offer what the API accepts because their options come from
 * the same place. Everything past that — trimming, whitespace-only text, the
 * exact character count — is decided by the API, and the sentences it returns
 * are rendered verbatim.
 *
 * The alternative is a schema here restating the server's rules, and that copy
 * is where the two drift: the server tightens a bound, the form keeps accepting
 * the old one, and a user meets a 400 that the form told them could not happen.
 *
 * The inputs are uncontrolled and read through FormData on submit. That is what
 * makes a rejection cost the user nothing: React holds no value it could
 * re-render away, so the text stays in the DOM exactly as typed.
 */
export function TicketForm() {
  const router = useRouter();
  const formRef = useRef<HTMLFormElement>(null);
  const { mutate, isPending, error } = useCreateTicket();

  const fieldErrors = error instanceof ApiError ? error.fieldErrors : {};

  // Only when the server named no field. A 400 that named one is already shown
  // against it, and repeating it at the top would say the same thing twice.
  const formError =
    error && Object.keys(fieldErrors).length === 0
      ? describeFailure(error)
      : null;

  function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const data = new FormData(event.currentTarget);
    const ticket = {
      title: String(data.get("title") ?? ""),
      description: String(data.get("description") ?? ""),
      category: String(data.get("category") ?? ""),
      priority: String(data.get("priority") ?? ""),
    } as NewTicket;

    mutate(ticket, {
      onSuccess: (created) => router.push(`/tickets/${created.id}`),

      // Field errors are announced when their field is focused, not shouted as
      // alerts — four of them at once would talk over each other. Moving focus
      // to the first one is what makes them reachable at all without sight.
      onError: (failure) => {
        if (!(failure instanceof ApiError)) return;

        const first = FIELDS.find((field) => failure.fieldErrors[field]);
        if (!first) return;

        const element = formRef.current?.elements.namedItem(first);
        if (element instanceof HTMLElement) element.focus();
      },
    });
  }

  return (
    <form
      ref={formRef}
      onSubmit={handleSubmit}
      // noValidate is NOT set: the browser's own required and maxlength checks
      // are the only client-side validation this form wants, and turning them
      // off would leave it with none.
      aria-busy={isPending}
      className="space-y-6"
    >
      {formError && (
        <p
          role="alert"
          className="rounded-md border border-destructive/50 px-4 py-3 text-sm text-destructive"
        >
          {formError}
        </p>
      )}

      <Field name="title" label="Title" error={fieldErrors.title}>
        {(props) => (
          <Input
            {...props}
            type="text"
            maxLength={MAX_TITLE_LENGTH}
            required
            autoComplete="off"
            placeholder="Cannot download my invoice"
          />
        )}
      </Field>

      <Field
        name="description"
        label="Description"
        error={fieldErrors.description}
        hint="What happened, and what you expected instead."
      >
        {(props) => (
          <Textarea
            {...props}
            rows={6}
            maxLength={MAX_DESCRIPTION_LENGTH}
            required
          />
        )}
      </Field>

      <div className="grid gap-6 sm:grid-cols-2">
        <Field name="category" label="Category" error={fieldErrors.category}>
          {(props) => (
            <Select {...props} required defaultValue="">
              <option value="" disabled>
                Choose one
              </option>
              {CATEGORIES.map((category) => (
                <option key={category} value={category}>
                  {humanise(category)}
                </option>
              ))}
            </Select>
          )}
        </Field>

        <Field name="priority" label="Priority" error={fieldErrors.priority}>
          {(props) => (
            <Select {...props} defaultValue={DEFAULT_PRIORITY}>
              {PRIORITIES.map((priority) => (
                <option key={priority} value={priority}>
                  {humanise(priority)}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>

      <Button type="submit" disabled={isPending}>
        {isPending ? "Creating…" : "Create ticket"}
      </Button>
    </form>
  );
}

/** The props a Field hands to whatever control it wraps. */
type ControlProps = {
  id: string;
  name: string;
  "aria-invalid": boolean;
  "aria-describedby": string | undefined;
};

/**
 * A labelled control with its error and hint wired up.
 *
 * It exists so the three ids that have to agree — the label's `htmlFor`, the
 * control's `id`, and whatever `aria-describedby` points at — are derived from
 * one name instead of typed four times. Getting one wrong breaks the label
 * association silently: the field still looks right and is simply unreachable
 * by name.
 */
function Field({
  name,
  label,
  error,
  hint,
  children,
}: {
  name: string;
  label: string;
  error?: string;
  hint?: string;
  children: (props: ControlProps) => React.ReactNode;
}) {
  const errorId = `${name}-error`;
  const hintId = `${name}-hint`;

  // The error wins when both are present: a hint restating what to type is
  // noise next to a sentence saying what was wrong with it.
  const describedBy = error ? errorId : hint ? hintId : undefined;

  return (
    <div className="space-y-2">
      <Label htmlFor={name}>{label}</Label>

      {children({
        id: name,
        name,
        "aria-invalid": Boolean(error),
        "aria-describedby": describedBy,
      })}

      {error ? (
        <p id={errorId} className="text-sm text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className="text-muted-foreground text-sm">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

/**
 * A native select.
 *
 * Native rather than a listbox built out of divs: it is keyboard operable,
 * screen-reader operable and touch-friendly without a line of JavaScript, and
 * it is what the platform hands to `required` and to FormData. A custom one
 * would be work spent re-earning behaviour we already have.
 */
function Select({
  className,
  ...props
}: React.ComponentProps<"select"> & { className?: string }) {
  return (
    <select
      {...props}
      className={
        "h-8 w-full rounded-lg border border-input bg-transparent px-2.5 py-1 " +
        "text-base transition-colors outline-none focus-visible:border-ring " +
        "focus-visible:ring-3 focus-visible:ring-ring/50 " +
        "aria-invalid:border-destructive aria-invalid:ring-3 " +
        "aria-invalid:ring-destructive/20 md:text-sm " +
        (className ?? "")
      }
    />
  );
}
