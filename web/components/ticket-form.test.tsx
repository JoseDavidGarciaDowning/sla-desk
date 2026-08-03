import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TicketForm } from "@/components/ticket-form";
import { ApiError, type Problem } from "@/lib/api";
import { MAX_DESCRIPTION_LENGTH, MAX_TITLE_LENGTH } from "@/lib/contract";
import { type Ticket, ticketKeys } from "@/lib/tickets";

// vi.mock is hoisted above the imports, so anything it closes over has to be
// hoisted with it or it is still in the temporal dead zone when the factory
// runs.
const { push, apiFetch } = vi.hoisted(() => ({
  push: vi.fn(),
  apiFetch: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push }),
}));

vi.mock("@/lib/use-api", () => ({
  useApiFetch: () => apiFetch,
}));

const created: Ticket = {
  id: "6f1b5f2a-0000-4000-8000-000000000001",
  title: "Cannot download my invoice",
  description: "The download button returns a 500.",
  category: "billing",
  priority: "normal",
  status: "open",
  sla_due_at: "2026-08-03T18:00:00Z",
  sla_breached: false,
  created_at: "2026-08-03T14:00:00Z",
  updated_at: "2026-08-03T14:00:00Z",
};

function problemError(status: number, errors?: Record<string, string>) {
  const problem: Problem = {
    type: "about:blank",
    title: "Bad Request",
    status,
    detail: "the request body failed validation",
    errors,
  };
  return new ApiError(status, "/api/tickets", problem);
}

function renderForm() {
  // Retries off: a mutation that retried would make the failure tests wait for
  // attempts that prove nothing, and the application turns them off too.
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  render(
    <QueryClientProvider client={client}>
      <TicketForm />
    </QueryClientProvider>,
  );

  return client;
}

/**
 * Fills every field with something the server would accept, optionally leaving
 * one of them alone.
 *
 * The omission is what makes the required-field tests mean anything: with all
 * four filled but one, only that field can be what blocks the submission.
 */
async function fillValidTicket(
  user: ReturnType<typeof userEvent.setup>,
  except?: string,
) {
  if (except !== "title") {
    await user.type(screen.getByLabelText(/title/i), created.title);
  }
  if (except !== "description") {
    await user.type(screen.getByLabelText(/description/i), created.description);
  }
  if (except !== "category") {
    await user.selectOptions(screen.getByLabelText(/category/i), "billing");
  }
}

function submit(user: ReturnType<typeof userEvent.setup>) {
  return user.click(screen.getByRole("button", { name: /create ticket/i }));
}

beforeEach(() => {
  apiFetch.mockReset();
  push.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("TicketForm", () => {
  // The bounds are the API's, reaching the markup through the generated
  // contract. Hardcoding them here would defeat the generator: the test would
  // agree with the component while both disagreed with the server.
  it("takes its limits from the generated contract", () => {
    renderForm();

    expect(screen.getByLabelText(/title/i)).toHaveProperty(
      "maxLength",
      MAX_TITLE_LENGTH,
    );
    expect(screen.getByLabelText(/description/i)).toHaveProperty(
      "maxLength",
      MAX_DESCRIPTION_LENGTH,
    );
  });

  it("offers every category the API accepts", () => {
    renderForm();

    const options = screen
      .getAllByRole("option")
      .map((option) => (option as HTMLOptionElement).value)
      .filter(Boolean);

    for (const category of ["billing", "technical", "account", "other"]) {
      expect(options).toContain(category);
    }
    for (const priority of ["urgent", "high", "normal", "low"]) {
      expect(options).toContain(priority);
    }
  });

  // The cheapest validation there is, and the only kind this component does:
  // the browser's own. Anything beyond required and maxlength would be a second
  // copy of rules that live in Go.
  //
  // One case per field, each with every other field filled. A single test that
  // submitted an entirely empty form would pass with `required` on any one of
  // them — it was written that way first, and dropping `required` from the
  // title changed nothing.
  it.each(["title", "description", "category"])(
    "does not reach the API when %s is the only empty field",
    async (field) => {
      const user = userEvent.setup();
      renderForm();

      await fillValidTicket(user, field);
      await submit(user);

      expect(apiFetch).not.toHaveBeenCalled();
    },
  );

  it("sends the filled fields to the API", async () => {
    const user = userEvent.setup();
    apiFetch.mockResolvedValue(created);
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    await waitFor(() => expect(apiFetch).toHaveBeenCalledTimes(1));
    const [path, init] = apiFetch.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/tickets");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      title: created.title,
      description: created.description,
      category: "billing",
      priority: "normal",
    });
  });

  // The point of carrying the problem document across. The component knows
  // nothing about what makes a title acceptable; it renders the sentence the
  // server wrote, against the field the server named.
  it("renders a server field error against the field it names", async () => {
    const user = userEvent.setup();
    apiFetch.mockRejectedValue(
      problemError(400, { title: "must be at most 200 characters" }),
    );
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    const message = await screen.findByText("must be at most 200 characters");

    const title = screen.getByLabelText(/title/i);
    expect(title).toHaveProperty("ariaInvalid", "true");
    expect(title.getAttribute("aria-describedby")).toBe(message.id);
  });

  // A rejection must not cost the user their work. The inputs are uncontrolled,
  // so React has no value to re-render away — this test is what keeps it that
  // way.
  it("keeps what the user typed when the server refuses", async () => {
    const user = userEvent.setup();
    apiFetch.mockRejectedValue(problemError(400, { title: "must not be empty" }));
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    await screen.findByText("must not be empty");

    expect(screen.getByLabelText(/description/i)).toHaveProperty(
      "value",
      created.description,
    );
    expect(screen.getByLabelText(/category/i)).toHaveProperty(
      "value",
      "billing",
    );
  });

  // A failure that names no field still has to say something. Silence would
  // look like the form ignoring the click.
  //
  // The status is asserted, not merely the presence of an alert: an alert that
  // said the wrong thing would satisfy findByRole and tell the user nothing
  // they could act on or report.
  it("reports a failure that names no field, with its status", async () => {
    const user = userEvent.setup();
    apiFetch.mockRejectedValue(new ApiError(500, "/api/tickets"));
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    expect((await screen.findByRole("alert")).textContent).toContain("500");
  });

  // A session that ended while the form was open. "The API answered 401" is
  // true and useless: it reads as a fault, and the user retries forever. The
  // page is deliberately not reloaded — that is what a failed query does, and a
  // query has no unsaved work to destroy.
  it("says the session ended on a 401, and keeps the draft", async () => {
    const user = userEvent.setup();
    apiFetch.mockRejectedValue(new ApiError(401, "/api/tickets"));
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/session/i);
    expect(alert.textContent).not.toContain("401");

    expect(screen.getByLabelText(/description/i)).toHaveProperty(
      "value",
      created.description,
    );
  });

  // A failure that never reached the API at all — a dropped connection, DNS,
  // an offline laptop. It has no status to report, and saying "500" would
  // send the user looking for a server problem that does not exist.
  it("reports a failure that never reached the API", async () => {
    const user = userEvent.setup();
    apiFetch.mockRejectedValue(new TypeError("Failed to fetch"));
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("could not be reached");
  });

  // Two tickets from one impatient double-click is a real failure: nothing
  // downstream deduplicates, so the customer ends up with a duplicate and an
  // agent has to close one.
  it("cannot be submitted twice", async () => {
    const user = userEvent.setup();
    apiFetch.mockImplementation(() => new Promise(() => {}));
    renderForm();

    await fillValidTicket(user);
    await submit(user);

    const button = screen.getByRole("button", { name: /creating|create ticket/i });
    await waitFor(() => expect(button).toHaveProperty("disabled", true));

    await user.click(button);
    expect(apiFetch).toHaveBeenCalledTimes(1);
  });

  // The reason there is no optimistic insert: by the time there is anything to
  // show, the server has already said what it is. Seeding beats guessing, and
  // needs no rollback.
  it("seeds the new ticket into the cache and goes to it", async () => {
    const user = userEvent.setup();
    apiFetch.mockResolvedValue(created);
    const client = renderForm();

    await fillValidTicket(user);
    await submit(user);

    await waitFor(() =>
      expect(client.getQueryData(ticketKeys.detail(created.id))).toEqual(
        created,
      ),
    );
    expect(push).toHaveBeenCalledWith(`/tickets/${created.id}`);
  });

  // The list has to be refetched, or the customer lands back on a page that
  // does not show the ticket they just made.
  //
  // The detail must NOT be, and that is the half worth guarding. Invalidating
  // ticketKeys.all reads as the obvious thing to write and passes the first
  // assertion here, because `all` is a prefix of `list`. It also marks the
  // ticket seeded a line earlier as stale, so the page it was seeded for
  // refetches on arrival and the seeding bought nothing. Nothing else in this
  // suite can see that.
  it("marks the list for refetching without undoing the seed", async () => {
    const user = userEvent.setup();
    apiFetch.mockResolvedValue(created);
    const client = renderForm();
    client.setQueryData(ticketKeys.list(), {
      tickets: [],
      next_cursor: null,
    });

    await fillValidTicket(user);
    await submit(user);

    await waitFor(() =>
      expect(client.getQueryState(ticketKeys.list())?.isInvalidated).toBe(true),
    );
    expect(
      client.getQueryState(ticketKeys.detail(created.id))?.isInvalidated,
    ).toBe(false);
  });
});
