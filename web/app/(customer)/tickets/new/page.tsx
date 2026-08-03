import Link from "next/link";

import { TicketForm } from "@/components/ticket-form";

export const metadata = {
  title: "New ticket · SLA Desk",
};

export default function NewTicketPage() {
  return (
    <div className="max-w-2xl space-y-8">
      <div className="space-y-2">
        <Link
          href="/tickets"
          className="text-muted-foreground hover:text-foreground text-sm"
        >
          ← Your tickets
        </Link>
        <h1 className="text-2xl font-semibold tracking-tight">New ticket</h1>
        <p className="text-muted-foreground text-sm">
          The SLA clock starts as soon as this is created, and how long it runs
          for depends on the priority.
        </p>
      </div>

      <TicketForm />
    </div>
  );
}
