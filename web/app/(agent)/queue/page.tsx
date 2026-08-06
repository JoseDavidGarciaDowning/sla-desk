import { AgentQueue } from "./agent-queue";
import { QueueFilters } from "./queue-filters";

export default function QueuePage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Queue</h1>
        {/* Worth saying on screen. The order is not visible from a list of
            rows, and an agent who assumes newest-first will read it wrong —
            the whole point is that the top row is the next breach. */}
        <p className="text-muted-foreground text-sm">
          Every ticket, soonest deadline first. Paused tickets sort last.
        </p>
      </div>

      <QueueFilters />
      <AgentQueue />
    </div>
  );
}
