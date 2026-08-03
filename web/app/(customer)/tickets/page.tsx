import Link from "next/link";

import { buttonVariants } from "@/components/ui/button";

import { TicketFilters } from "./ticket-filters";
import { TicketList } from "./ticket-list";

export default function TicketsPage() {
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight">Your tickets</h1>
        {/* A link, not a Button: this navigates. Base UI's Button composes
            through a `render` prop rather than shadcn's `asChild`, but wrapping
            an anchor in either only buys the styling — and buttonVariants gives
            that directly, without the element lying about what it does. */}
        <Link href="/tickets/new" className={buttonVariants()}>
          New ticket
        </Link>
      </div>

      <TicketFilters />
      <TicketList />
    </div>
  );
}
