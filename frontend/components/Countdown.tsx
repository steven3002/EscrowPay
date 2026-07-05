"use client";

import { useEffect, useState } from "react";
import { countdown } from "@/lib/format";

// Countdown ticks a deadline down once a second. The deadline is a server
// timestamp; the component only renders the remaining time and never decides
// what happens when it lapses — the server transition does, surfaced on the next
// refetch.
export function Countdown({ deadline, label }: { deadline: string; label: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const { text, lapsed } = countdown(deadline, now);
  return (
    <div className="flex items-baseline justify-between gap-4">
      <span className="text-sm text-muted">{label}</span>
      <span
        className={`font-mono text-sm font-semibold tabular-nums ${
          lapsed ? "text-amber-600" : "text-foreground"
        }`}
      >
        {lapsed ? "processing…" : text}
      </span>
    </div>
  );
}
