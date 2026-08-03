import { clerkMiddleware } from "@clerk/nextjs/server";

/**
 * Clerk's request hook.
 *
 * `proxy.ts`, not `middleware.ts`. Next 16 renamed the convention and the
 * exported function; a `middleware.ts` here would be ignored without warning,
 * leaving the app with no auth state at all.
 *
 * **This protects nothing, deliberately.** `clerkMiddleware()` on its own
 * attaches auth state to the request and returns — closing a route needs
 * `createRouteMatcher` plus `auth.protect()` inside a callback. The redirect
 * lives in `app/(customer)/layout.tsx` instead, for two reasons:
 *
 *  - Next's own documentation for this version says Proxy "should not be used
 *    as a full session management or authorization solution", and Clerk
 *    publishes a migration guide away from `createRouteMatcher` whose stated
 *    goal is to move protection to individual resources.
 *  - A layout covers every route in its group, including ones added later. A
 *    matcher is a list of patterns somebody has to remember to update, and a
 *    forgotten one fails silently — the route simply stops being protected.
 *
 * None of this is the security boundary regardless. This app holds no data of
 * its own: every ticket read goes through the Go API, which rejects
 * unauthenticated requests and scopes every query by requester in SQL. What
 * happens here is UX — a signed-out visitor is sent to sign in rather than
 * shown an empty shell.
 */
/**
 * signInUrl and signUpUrl belong here, not only on `<ClerkProvider>`.
 *
 * `auth.protect()` runs on the server, and the provider's props are React
 * context that the server never sees. With them set only there, the redirect
 * went to Clerk's hosted pages on the accounts.dev domain and the sign-in route
 * in this app was dead code. The 307 looked correct either way — only the
 * Location header said otherwise.
 */
export default clerkMiddleware({
  signInUrl: "/sign-in",
  signUpUrl: "/sign-up",
});

export const config = {
  matcher: [
    // Everything except Next's internals and static files.
    "/((?!_next|[^?]*\\.(?:html?|css|js(?!on)|jpe?g|webp|png|gif|svg|ttf|woff2?|ico|csv|docx?|xlsx?|zip|webmanifest)).*)",
    // Always, for API routes and Clerk's own frontend endpoints.
    "/(api|trpc)(.*)",
    "/__clerk/(.*)",
  ],
};
