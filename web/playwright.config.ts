import { defineConfig, devices } from "@playwright/test";

// Locally the Clerk keys live in .env.local, which Next reads and Playwright
// does not — clerkSetup() looks them up in process.env. Node's own loader, so
// there is no dotenv dependency for one line. In CI the file does not exist and
// the values arrive as repository secrets, which is why this is not fatal.
try {
  process.loadEnvFile(".env.local");
} catch {
  // No .env.local. Expected in CI; anywhere else, global.setup.ts says so
  // clearly when the key turns out to be missing.
}

/**
 * End-to-end tests, against a stack this file starts.
 *
 * **They run against a Clerk _development_ instance, never production.** The
 * sign-up path needs `+clerk_test` addresses and the `424242` code, and those
 * only work while Clerk's test mode is on. Test mode is on by default in a
 * development instance; enabling it on a production one is possible and Clerk
 * calls it "highly discouraged", for a concrete reason — it lets anybody who
 * knows the pattern sign up bypassing email verification, on the live
 * application.
 *
 * What watches production instead is `smoke/production_test.go`: reads only,
 * no credentials, safe to run on every push and on a schedule.
 *
 * The Go API is **not** started here. It needs Postgres, migrations and its own
 * secrets, which is the CI job's business (and `make up && make api` locally).
 * Starting it from a Playwright config would hide that dependency behind a test
 * runner.
 */
const webPort = Number(process.env.E2E_WEB_PORT ?? 3000);
const baseURL = process.env.E2E_BASE_URL ?? `http://localhost:${webPort}`;

export default defineConfig({
  testDir: "./e2e",

  // Off. These tests sign a user up and then look for the ticket that user
  // created; two workers would race for the same rendered list.
  fullyParallel: false,
  workers: 1,

  // A test that only passes on the second attempt is a test that fails, and in
  // CI a retry would hide exactly the flakiness worth knowing about.
  retries: 0,

  // .only left in a committed file makes CI green by running almost nothing.
  forbidOnly: !!process.env.CI,

  reporter: process.env.CI ? [["github"], ["list"]] : "list",

  use: {
    baseURL,
    // Only on failure, and only for the run that failed — enough to see what
    // the page looked like without storing a video of every green run.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },

  projects: [
    {
      // Fetches Clerk's Testing Token, which is what gets the suite past bot
      // protection. Without it the sign-up form refuses a headless browser and
      // the failure looks like a broken selector.
      name: "setup",
      testMatch: /global\.setup\.ts/,
    },
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
      dependencies: ["setup"],
    },
  ],

  // Production build rather than the dev server: it is what gets deployed, and
  // the dev server hides build-time problems — NEXT_PUBLIC_ values are inlined
  // at build, so a dev run can pass with an environment a build would not.
  webServer: {
    command: `pnpm build && pnpm start --port ${webPort}`,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
    stdout: "pipe",
    stderr: "pipe",
  },
});
