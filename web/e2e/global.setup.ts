import { clerkSetup } from "@clerk/testing/playwright";
import { test as setup } from "@playwright/test";

// Serial, because every test in the run depends on the token this obtains.
setup.describe.configure({ mode: "serial" });

/**
 * Obtains Clerk's Testing Token for the run.
 *
 * Clerk applies bot protection to sign-up and sign-in, and a headless browser
 * looks exactly like what that protection exists to stop. The token is what
 * exempts this run — without it the form simply refuses to proceed, and the
 * failure reads as a broken selector rather than as a bot check.
 *
 * It reads CLERK_SECRET_KEY and NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY from the
 * environment. Those must belong to a **development** instance: see the comment
 * at the top of playwright.config.ts for why production is not an option here.
 */
setup("obtain a Clerk testing token", async () => {
  const key = process.env.CLERK_SECRET_KEY;

  if (!key) {
    throw new Error(
      "CLERK_SECRET_KEY is not set. The end-to-end tests need a Clerk " +
        "development instance — locally that is web/.env.local, in CI it is a " +
        "repository secret.",
    );
  }

  if (key.startsWith("sk_live_")) {
    throw new Error(
      "CLERK_SECRET_KEY is a production key (sk_live_). These tests sign up " +
        "with +clerk_test addresses, which requires Clerk's test mode, and " +
        "turning test mode on in production lets anyone bypass email " +
        "verification on the live application. Use a development key.",
    );
  }

  await clerkSetup();
});
