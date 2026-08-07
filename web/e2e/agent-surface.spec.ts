import { setupClerkTestingToken } from "@clerk/testing/playwright";
import { expect, test } from "@playwright/test";

const TEST_OTP = "424242";

/**
 * The agent surface, as a customer meets it.
 *
 * This suite deliberately covers the *negative* half of slice 2, and the reason
 * is a limit rather than a preference: a new account is created on every run
 * and every account starts as a customer (docs/spec.md §4.5). Becoming an agent
 * means appearing in AGENT_CLERK_USER_IDS, which the API reads at startup —
 * long before this run exists, and with an id that does not exist until the
 * sign-up below completes.
 *
 * So what is provable here is what nobody could prove without a browser: that a
 * signed-in customer who types /queue is turned away, and that the route exists
 * to turn them away rather than 404ing by accident.
 *
 * The positive half — an agent opening the queue, taking a ticket and pausing
 * the clock — is covered by integration and component tests, and closing that
 * last gap needs a seeded agent whose Clerk id is known before the API boots.
 * The cheapest way in is a fixture account created once in the development
 * instance and listed in the workflow's environment; it is recorded in T25
 * rather than built, because a shared account that lives between runs is a
 * different kind of test with its own failure modes.
 */
test("a signed-in customer is turned away from the agent surface", async ({
  page,
}) => {
  // Without this, Clerk's bot protection refuses a headless browser and the
  // failure reads as a missing selector.
  await setupClerkTestingToken({ page });

  const email = `sla-desk+clerk_test_${Date.now()}@example.com`;

  await test.step("sign up with a fresh account", async () => {
    await page.goto("/sign-up");
    await page.waitForSelector(".cl-signUp-root", { state: "attached" });

    await page.locator("input[name=emailAddress]").fill(email);
    await page.locator("input[name=password]").fill("Correct-Horse-Battery-9");
    await page.getByRole("button", { name: "Continue", exact: true }).click();

    await page.waitForResponse(
      (r) => r.url().includes("prepare_verification") && r.status() === 200,
    );

    await page
      .getByRole("textbox", { name: /verification code/i })
      .pressSequentially(TEST_OTP);

    await page.waitForURL("**/tickets", { timeout: 30_000 });
  });

  await test.step("the queue redirects a customer to their own tickets", async () => {
    // `waitUntil: "commit"` rather than the default "load", and the navigation
    // error is tolerated on purpose.
    //
    // Signed out, the redirect comes from the proxy before anything renders and
    // page.goto resolves normally. Signed in it comes from the layout, which
    // asks the API what our database says this person is and then redirects —
    // during the render, so the document being fetched is replaced mid-flight
    // and Chromium reports net::ERR_ABORTED for the original navigation.
    //
    // That abort *is* the redirect happening. What this test cares about is
    // where the browser ended up, which the assertion below is what checks.
    await page.goto("/queue", { waitUntil: "commit" }).catch((error: Error) => {
      if (!error.message.includes("ERR_ABORTED")) throw error;
    });

    // Redirected, not shown an error. The layout asks the API what our own
    // database says this person is — Clerk holds no role — and sends anyone
    // who is not staff back where they belong.
    await page.waitForURL("**/tickets", { timeout: 30_000 });

    // And none of the agent chrome rendered on the way. A client-side check
    // would flash it before redirecting, which is the reason that check runs
    // on the server.
    await expect(page.getByRole("heading", { name: /queue/i })).toHaveCount(0);
  });

  await test.step("a ticket detail in the agent surface is refused too", async () => {
    // Any well-formed uuid. The layout turns this away before the id is ever
    // looked up, which is the point: a customer must not be able to probe
    // whether an id names a real ticket by watching the two answers differ.
    await page
      .goto("/queue/11111111-2222-3333-4444-555555555555", {
        waitUntil: "commit",
      })
      .catch((error: Error) => {
        if (!error.message.includes("ERR_ABORTED")) throw error;
      });

    await page.waitForURL("**/tickets", { timeout: 30_000 });
  });
});

/**
 * Signed out, the agent surface behaves like the customer one: it sends you to
 * sign in rather than answering 404.
 *
 * A 404 would be a route that does not exist, and this suite is the only place
 * that can tell the difference from outside — every other test in the project
 * builds the router itself and therefore knows the answer in advance.
 */
test("signed out, the agent surface sends you to sign in", async ({ page }) => {
  await setupClerkTestingToken({ page });

  await page.goto("/queue");

  await page.waitForURL(/sign-in/, { timeout: 30_000 });

  // Our own sign-in route, not Clerk's hosted one. T12 shipped a correct 307
  // that pointed at accounts.dev — a page that is not part of this app — so the
  // status alone was never evidence, and neither is "a sign-in form appeared".
  expect(new URL(page.url()).hostname).not.toContain("accounts.dev");
  await expect(page.locator(".cl-signIn-root")).toBeVisible({ timeout: 30_000 });
});
