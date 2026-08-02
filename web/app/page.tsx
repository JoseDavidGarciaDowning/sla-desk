import { ApiStatus } from "@/components/api-status";
import { Button } from "@/components/ui/button";

/**
 * Placeholder landing page for slice 1.
 *
 * It proves the toolchain end to end: App Router, Tailwind and shadcn/ui all
 * render, and the browser reaches the API on Cloud Run across origins. T12
 * replaces this with the real customer portal entry point.
 *
 * There is deliberately no link to /tickets yet. Next.js prefetches links on
 * sight, so a link to an unbuilt route produces a 404 in the console before
 * anyone clicks anything.
 */
export default function Home() {
  return (
    <main className="mx-auto flex w-full max-w-2xl flex-1 flex-col justify-center gap-8 px-6 py-16">
      <div className="space-y-3">
        <p className="text-sm font-medium tracking-widest text-muted-foreground uppercase">
          Slice 1 · scaffold
        </p>
        <h1 className="text-4xl font-semibold tracking-tight text-balance">
          SLA Desk
        </h1>
        <p className="text-lg text-pretty text-muted-foreground">
          Customers raise tickets. Agents triage, assign and answer them before
          the clock runs out — and the clock pauses while it is the customer’s
          turn to reply.
        </p>
      </div>

      <ApiStatus />

      <div className="flex flex-wrap gap-3">
        <Button
          size="lg"
          variant="outline"
          nativeButton={false}
          render={
            <a
              href="https://github.com/JoseDavidGarciaDowning/sla-desk"
              target="_blank"
              rel="noreferrer noopener"
            />
          }
        >
          Read the design decisions
        </Button>
      </div>

      <p className="text-sm text-muted-foreground">
        The customer portal is not built yet — that is task T12.
      </p>
    </main>
  );
}
