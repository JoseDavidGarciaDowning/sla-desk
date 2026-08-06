import { auth } from "@clerk/nextjs/server";
import Link from "next/link";
import { redirect } from "next/navigation";
import { UserButton } from "@clerk/nextjs";

import { apiFetch } from "@/lib/api";
import { type AgentMe, isStaff } from "@/lib/agent";

/**
 * The agent shell, and the one place a customer is turned away from it.
 *
 * Two checks, and they answer different questions. `auth.protect()` asks
 * whether anyone is signed in; the fetch below asks what our own database says
 * they are — Clerk holds no role (docs/spec.md §4.3), so the session cannot
 * answer the second one.
 *
 * **Neither is the security boundary**, exactly as in the customer layout. The
 * API refuses every /api/agent request that does not carry an agent's role, and
 * a customer who forces this URL gets nothing from it either way. What this
 * prevents is being shown a queue-shaped shell that will never fill.
 *
 * Done on the server rather than in a client component so a customer never sees
 * the agent chrome flash before the redirect. It costs one request per
 * navigation into the group, against Cloud Run's own /api/agent/me — the
 * cheapest endpoint in the API.
 */
export default async function AgentLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  await auth.protect();

  const { getToken } = await auth();
  const token = await getToken();

  let me: AgentMe | undefined;
  try {
    me = await apiFetch<AgentMe>("/api/agent/me", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      // The role decides what this person may see, so a cached answer would
      // outlive a promotion or survive a demotion. It is one small request.
      cache: "no-store",
    });
  } catch {
    // Any failure lands here, and they are deliberately not told apart. A 403
    // means "not staff"; an unreachable API means we cannot know — and showing
    // the agent shell on "cannot know" is the one outcome worth avoiding.
    // Failing closed sends a real agent to their own tickets during an outage,
    // which is recoverable; failing open shows a customer a queue that errors
    // on every request.
    redirect("/tickets");
  }

  if (!isStaff(me.role)) {
    redirect("/tickets");
  }

  return (
    <div className="flex min-h-full flex-col">
      <header className="border-b">
        <div className="mx-auto flex w-full max-w-6xl items-center justify-between px-6 py-4">
          <div className="flex items-baseline gap-3">
            <Link href="/queue" className="font-semibold tracking-tight">
              SLA Desk
            </Link>
            {/* The role is worth showing. An admin and an agent see the same
                queue today, and knowing which account you are signed in as is
                what stops "why can I not do this" being a mystery. */}
            <span className="text-muted-foreground text-xs uppercase tracking-wide">
              {me.role}
            </span>
          </div>
          <div className="flex items-center gap-4">
            <Link
              href="/tickets"
              className="text-muted-foreground text-sm hover:underline"
            >
              My tickets
            </Link>
            <UserButton />
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl flex-1 px-6 py-10">
        {children}
      </main>
    </div>
  );
}
