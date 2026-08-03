import { auth } from "@clerk/nextjs/server";
import Link from "next/link";
import { UserButton } from "@clerk/nextjs";

/**
 * The customer portal shell, and the one place signed-out visitors are turned
 * away.
 *
 * `auth.protect()` redirects to the sign-in route. It lives here rather than in
 * proxy.ts on purpose — see the comment there — and it covers every route in
 * this group, including ones added after this was written.
 *
 * It is not the security boundary. This app reads nothing directly; the Go API
 * refuses unauthenticated requests and scopes every query by requester in SQL.
 * What this prevents is a signed-out visitor being shown an empty shell.
 */
export default async function CustomerLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  await auth.protect();

  return (
    <div className="flex min-h-full flex-col">
      <header className="border-b">
        <div className="mx-auto flex w-full max-w-4xl items-center justify-between px-6 py-4">
          <Link href="/tickets" className="font-semibold tracking-tight">
            SLA Desk
          </Link>
          <UserButton />
        </div>
      </header>

      <main className="mx-auto w-full max-w-4xl flex-1 px-6 py-10">
        {children}
      </main>
    </div>
  );
}
