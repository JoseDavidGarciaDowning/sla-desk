import { SignIn } from "@clerk/nextjs";

/**
 * Clerk's hosted sign-in, rendered in place.
 *
 * The catch-all segment is required: Clerk routes its own sub-steps —
 * verification, factor two, recovery — underneath this path.
 */
export default function SignInPage() {
  return (
    <main className="flex flex-1 items-center justify-center px-6 py-16">
      <SignIn />
    </main>
  );
}
