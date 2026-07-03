import type { Role } from "./api";

// The backend has no cross-pocket session (each pocket link is self-authorizing),
// so a participant's "my pockets" list is reconstructed client-side from the
// links they have opened on this device. This is a demo convenience layer, not a
// source of truth: every screen still refetches state from the server.

const KEY = "escrowpay.recent";

export interface RecentPocket {
  shortCode: string;
  pocketId?: string;
  role: Role;
  token: string;
  item: string;
  savedAt: number;
}

function read(): RecentPocket[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as RecentPocket[]) : [];
  } catch {
    return [];
  }
}

function write(list: RecentPocket[]) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(KEY, JSON.stringify(list.slice(0, 30)));
}

// remember upserts a pocket by (shortCode, role), moving it to the top.
export function remember(entry: Omit<RecentPocket, "savedAt">) {
  const list = read().filter(
    (p) => !(p.shortCode === entry.shortCode && p.role === entry.role),
  );
  list.unshift({ ...entry, savedAt: Date.now() });
  write(list);
}

export function listRecent(): RecentPocket[] {
  return read().sort((a, b) => b.savedAt - a.savedAt);
}

export function forget(shortCode: string, role: Role) {
  write(read().filter((p) => !(p.shortCode === shortCode && p.role === role)));
}
