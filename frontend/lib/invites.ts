import type { Role } from "./api";

// Counterparty invite links are minted exactly once — when a pocket is created,
// or when a broker converts a deal — and the server keeps only a hash of each,
// so it can never hand the raw link back. This stash keeps the links this
// browser minted, keyed by (short code, role), so the creator or broker can
// copy an unclaimed seat's link again from the pocket page. Like recent.ts it's
// a demo-grade, device-local convenience, never a source of truth: the server
// still decides (via pending_invites) which seats are worth re-sharing.

const KEY = "escrowpay.invites";

type InviteMap = Record<string, Partial<Record<Role, string>>>;

function read(): InviteMap {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as InviteMap) : {};
  } catch {
    return {};
  }
}

function write(map: InviteMap) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(KEY, JSON.stringify(map));
}

// rememberInvite stashes the shareable path for one counterparty seat.
export function rememberInvite(shortCode: string, role: Role, path: string) {
  const map = read();
  map[shortCode] = { ...map[shortCode], [role]: path };
  write(map);
}

// getInvite returns the stashed share path for a seat, or null if this browser
// never held it.
export function getInvite(shortCode: string, role: Role): string | null {
  return read()[shortCode]?.[role] ?? null;
}
