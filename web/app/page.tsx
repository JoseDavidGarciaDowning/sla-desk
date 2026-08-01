import Link from "next/link";

import { Button } from "@/components/ui/button";

/**
 * Placeholder landing page for slice 1.
 *
 * It exists to prove the toolchain end to end: App Router, Tailwind and a
 * shadcn/ui component all render. T3 adds a live API status check here; T12
 * replaces it with the real customer portal entry point.
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

      {/*
        shadcn's Button wraps Base UI, not Radix. There is no `asChild` here:
        composition goes through `render`, and `nativeButton={false}` tells Base
        UI the rendered element is an anchor rather than a <button>, so it keeps
        link semantics and keyboard behaviour instead of button ones.
      */}
      <div className="flex flex-wrap gap-3">
        <Button size="lg" render={<Link href="/tickets" />} nativeButton={false}>
          Open the customer portal
        </Button>
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
