import { TicketList } from "./ticket-list";

export default function TicketsPage() {
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Your tickets</h1>
      <TicketList />
    </div>
  );
}
