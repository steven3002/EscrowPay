import type { PocketState } from "./api";

// formatKobo renders an integer kobo amount as Nigerian naira. Amounts are held
// as integer kobo end to end; this is the only place they become a display
// string.
export function formatKobo(kobo: number | undefined): string {
  if (kobo === undefined) return "—";
  return new Intl.NumberFormat("en-NG", {
    style: "currency",
    currency: "NGN",
    minimumFractionDigits: 2,
  }).format(kobo / 100);
}

export function formatDateTime(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString("en-NG", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const STATE_LABELS: Record<PocketState, string> = {
  DRAFT: "Draft",
  CREATED: "Awaiting payment",
  FUNDED: "Funds secured",
  DELIVERED_PENDING: "Delivered — inspecting",
  SETTLED: "Settled",
  DISPUTED: "In dispute",
  FROZEN: "Frozen",
  REFUNDED: "Refunded",
  CANCELLED: "Cancelled",
  EXPIRED: "Expired",
};

export function stateLabel(state: PocketState): string {
  return STATE_LABELS[state] ?? state;
}

// stateTone maps a pocket state to a Tailwind color group for badges: emerald
// for money-secured/settled, amber for time-pressured, red for adverse, zinc for
// neutral/terminal.
export function stateTone(state: PocketState): "emerald" | "amber" | "red" | "zinc" | "blue" {
  switch (state) {
    case "FUNDED":
    case "SETTLED":
      return "emerald";
    case "CREATED":
    case "DELIVERED_PENDING":
      return "blue";
    case "FROZEN":
      return "amber";
    case "DISPUTED":
    case "EXPIRED":
      return "red";
    default:
      return "zinc";
  }
}

// countdown renders the time remaining until an ISO deadline relative to now, or
// a lapsed marker. Returned as a short human string.
export function countdown(deadlineIso: string, nowMs: number): { text: string; lapsed: boolean } {
  const remaining = new Date(deadlineIso).getTime() - nowMs;
  if (remaining <= 0) return { text: "lapsed", lapsed: true };
  const s = Math.floor(remaining / 1000);
  const days = Math.floor(s / 86400);
  const hours = Math.floor((s % 86400) / 3600);
  const mins = Math.floor((s % 3600) / 60);
  const secs = s % 60;
  if (days > 0) return { text: `${days}d ${hours}h`, lapsed: false };
  if (hours > 0) return { text: `${hours}h ${mins}m`, lapsed: false };
  if (mins > 0) return { text: `${mins}m ${secs}s`, lapsed: false };
  return { text: `${secs}s`, lapsed: false };
}
