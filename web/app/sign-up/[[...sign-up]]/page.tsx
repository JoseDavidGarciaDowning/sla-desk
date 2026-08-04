import { SignUp } from "@clerk/nextjs";

/**
 * fallbackRedirectUrl, not forceRedirectUrl.
 *
 * Without it Clerk's default sends a newly signed-in user to `/` — the
 * marketing page — rather than into the application they just authenticated to
 * reach. "fallback" is the right one because it only applies when there is no
 * redirect_url on the request: someone who deep-linked to a ticket, was bounced
 * to sign in, and came back still lands on that ticket. `force` would send them
 * to the list instead and quietly lose where they were going.
 */
export default function SignUpPage() {
  return (
    <main className="flex flex-1 items-center justify-center px-6 py-16">
      <SignUp fallbackRedirectUrl="/tickets" />
    </main>
  );
}
