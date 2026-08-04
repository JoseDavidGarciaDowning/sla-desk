import { setupClerkTestingToken } from "@clerk/testing/playwright";
import { expect, test } from "@playwright/test";

/**
 * The one path this application exists for, end to end and unmocked.
 *
 * Every other test in the repository mocks a boundary: the component tests mock
 * `useApiFetch`, the handler tests use a fake store, the integration tests use a
 * real database but no browser. This one crosses all of them — a real browser,
 * a real Clerk instance, a real HTTP call to the Go API, and a real row in
 * Postgres — and it is the only test that would notice the pieces disagreeing
 * about how they connect.
 *
 * The work is split into `test.step` blocks so a failure names the step rather
 * than a line number: "sign up" failing and "create the ticket" failing are
 * completely different problems, and the report should say which happened
 * without anyone reading the trace first.
 */

/** Clerk's fixed code for `+clerk_test` addresses in test mode. */
const TEST_OTP = "424242";

const TICKET = {
  title: "Invoice download returns a 500",
  description:
    "Clicking download on the March invoice returns a 500. It worked last month.",
  category: "billing",
  priority: "urgent",
};

test("a customer signs up, raises a ticket, and sees it listed", async ({
  page,
}) => {
  // Without this, Clerk's bot protection refuses a headless browser and the
  // failure looks like a missing selector rather than what it is.
  await setupClerkTestingToken({ page });

  // A fresh address per run, so a re-run is not a duplicate sign-up and the
  // ticket list starts empty — the assertion below depends on that.
  const email = `sla-desk+clerk_test_${Date.now()}@example.com`;

  await test.step("sign up with a fresh account", async () => {
    await page.goto("/sign-up");
    await page.waitForSelector(".cl-signUp-root", { state: "attached" });

    await page.locator("input[name=emailAddress]").fill(email);
    await page.locator("input[name=password]").fill("Correct-Horse-Battery-9");
    await page.getByRole("button", { name: "Continue", exact: true }).click();

    // Clerk prepares the verification before the code field is usable. Waiting
    // for the response rather than for the field avoids typing into an input
    // that is about to be re-rendered.
    await page.waitForResponse(
      (r) => r.url().includes("prepare_verification") && r.status() === 200,
    );

    await page
      .getByRole("textbox", { name: /verification code/i })
      .pressSequentially(TEST_OTP);
  });

  await test.step("land in the application, not on the marketing page", async () => {
    // fallbackRedirectUrl on <SignUp>. Clerk's own default is "/", which would
    // drop a newly signed-up customer on the landing page.
    await page.waitForURL("**/tickets", { timeout: 30_000 });

    // The list renders exactly one of three things, and waiting only for the
    // empty state reports the wrong cause when it renders the third: the first
    // run of this suite was on a port the API's CORS_ALLOWED_ORIGIN did not
    // permit, and the failure read as "no tickets yet was not found" rather
    // than "the browser could not reach the API".
    const empty = page.getByText(/no tickets yet/i);
    // Scoped to <main>. Clerk renders its own role="alert" elements in the
    // development banner, and an unscoped lookup matched one of those — the
    // message it produced was empty, which is worse than no message.
    const failed = page.getByRole("main").getByRole("alert").first();

    await expect(empty.or(failed)).toBeVisible({ timeout: 30_000 });

    if (await failed.isVisible()) {
      throw new Error(
        `The ticket list could not reach the API: "${await failed.textContent()}".\n\n` +
          "Usually CORS_ALLOWED_ORIGIN on the Go API disagrees with the origin " +
          "this suite is served from, or the API is not running. The web server " +
          `is on ${page.url()}.`,
      );
    }
  });

  await test.step("raise a ticket", async () => {
    await page.getByRole("link", { name: /new ticket/i }).click();
    await page.waitForURL("**/tickets/new");

    await page.getByLabel(/title/i).fill(TICKET.title);
    await page.getByLabel(/description/i).fill(TICKET.description);
    await page.getByLabel(/category/i).selectOption(TICKET.category);
    await page.getByLabel(/priority/i).selectOption(TICKET.priority);

    await page.getByRole("button", { name: /create ticket/i }).click();
  });

  await test.step("arrive on the new ticket, with its SLA clock running", async () => {
    // A UUID, not "new" — proves the API assigned an id and the form navigated
    // to the ticket it actually created.
    await page.waitForURL(/\/tickets\/[0-9a-f-]{36}$/, { timeout: 30_000 });

    await expect(
      page.getByRole("heading", { name: TICKET.title }),
    ).toBeVisible();

    // Urgent buys a one-hour budget, and the clock starts on creation. Any
    // running countdown is enough here — the exact arithmetic is internal/sla's
    // to prove, and it does, exhaustively.
    await expect(page.getByText(/left$/)).toBeVisible();

    // The history the SLA clock is rebuilt from. One entry, the creation.
    await expect(page.getByRole("listitem")).toHaveCount(1);
    await expect(page.getByText(/opened it as open/i)).toBeVisible();
  });

  await test.step("see it in the list", async () => {
    await page.getByRole("link", { name: /your tickets/i }).click();
    await page.waitForURL("**/tickets");

    // Present because the create invalidated the list cache. Without that it
    // would still be showing the empty state it cached a moment ago.
    await expect(page.getByRole("link", { name: TICKET.title })).toBeVisible({
      timeout: 30_000,
    });
  });

  await test.step("a filtered URL survives a reload", async () => {
    await page.getByLabel(/priority/i).selectOption(TICKET.priority);
    await page.waitForURL(/priority=urgent/);

    await page.reload();

    // The control reads the URL, so it comes back set rather than reset to
    // "Any" — which is what makes a filtered view a link worth sharing.
    await expect(page.getByLabel(/priority/i)).toHaveValue(TICKET.priority);
    await expect(page.getByRole("link", { name: TICKET.title })).toBeVisible();
  });
});
