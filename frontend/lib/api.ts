// Single typed fetch layer for the EscrowPay API. Every network call in the app
// goes through here; components never call fetch directly. Requests are
// same-origin ("/api/...") and proxied to the Go backend by next.config
// rewrites, so no CORS handling is needed. Link-token auth is passed as the
// X-Link-Token header.

export type Role = "buyer" | "vendor" | "broker";
export type Structure = "p2p" | "brokered";
export type Mode = "instant" | "cooldown";

export type PocketState =
  | "DRAFT"
  | "CREATED"
  | "FUNDED"
  | "DELIVERED_PENDING"
  | "SETTLED"
  | "DISPUTED"
  | "FROZEN"
  | "REFUNDED"
  | "CANCELLED"
  | "EXPIRED";

export interface Money {
  currency: string;
  buyer_total_kobo?: number;
  amount_kobo?: number;
  commission_kobo?: number;
  premium_kobo?: number;
}

export interface Timers {
  delivery_deadline?: string;
  settle_after?: string;
  grace_deadline?: string;
  funding_expires_at?: string;
}

export interface Counterparty {
  role: Role;
  display_name: string;
  delivery_address?: string;
}

export interface PocketView {
  id: string;
  short_code: string;
  state: PocketState;
  structure: Structure;
  mode: Mode;
  your_role: Role;
  you: { claimed: boolean; accepted: boolean };
  item: { description: string; category: string };
  money: Money;
  counterparty?: Counterparty;
  timers: Timers;
  funding_url?: string;
  created_at: string;
}

export interface CreateRequest {
  structure: Structure;
  creator_role: Role;
  mode: Mode;
  inspection_window_minutes: number;
  delivery_window_minutes: number;
  amount_kobo: number;
  commission_kobo: number;
  premium_kobo: number;
  item_description: string;
  category: string;
  creator: { phone: string; display_name: string };
}

export interface CreateResponse {
  pocket_id: string;
  short_code: string;
  creator_role: Role;
  counterparty_role?: Role;
  share_url?: string;
  tokens: Partial<Record<Role, string>>;
}

export interface CodeEntryResult {
  accepted: boolean;
  state: PocketState;
  locked: boolean;
  attempts_remaining: number;
}

export interface EvidenceItem {
  id: string;
  party: Role;
  type: string;
  captured_at: string;
  within_window?: boolean;
  created_at?: string;
}

export interface DisputeView {
  pocket_id: string;
  class: string;
  opened_by: string;
  state: string;
  resolution?: string;
  created_at: string;
  evidence: EvidenceItem[];
}

export interface DisputeQueueItem {
  pocket_id: string;
  short_code: string;
  state: PocketState;
  class: string;
  opened_by: string;
  created_at: string;
}

export interface AdminParticipant {
  role: Role;
  display_name?: string;
  phone?: string;
  claimed: boolean;
  accepted: boolean;
}

export interface AdminEvent {
  id: number;
  actor: string;
  from_state?: string;
  to_state?: string;
  kind: string;
  created_at: string;
}

export interface AdminDetail {
  id: string;
  short_code: string;
  state: PocketState;
  structure: Structure;
  mode: Mode;
  item: { description: string; category: string };
  money: Money;
  delivery_address?: string;
  timers: Timers;
  participants: AdminParticipant[];
  events: AdminEvent[];
  dispute?: {
    class: string;
    opened_by: string;
    state: string;
    resolution?: string;
    created_at: string;
  };
  evidence: EvidenceItem[];
  created_at: string;
}

// ApiError carries the HTTP status so callers can branch on 401/403/404/409/423.
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

async function request<T>(
  method: string,
  path: string,
  opts: { token?: string; body?: unknown; raw?: BodyInit; headers?: Record<string, string> } = {},
): Promise<T> {
  const headers: Record<string, string> = { ...(opts.headers ?? {}) };
  if (opts.token) headers["X-Link-Token"] = opts.token;
  let body: BodyInit | undefined = opts.raw;
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(`/api${path}`, { method, headers, body, cache: "no-store" });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    const message = (data && (data.error as string)) || res.statusText || "request failed";
    throw new ApiError(res.status, message);
  }
  return data as T;
}

export const api = {
  createPocket: (req: CreateRequest) =>
    request<CreateResponse>("POST", "/pockets", { body: req }),

  getPocket: (shortCode: string, token: string) =>
    request<PocketView>("GET", `/p/${shortCode}`, { token }),

  claim: (shortCode: string, token: string, phone: string, displayName: string) =>
    request<PocketView>("POST", `/p/${shortCode}/claim`, {
      token,
      body: { phone, display_name: displayName },
    }),

  accept: (shortCode: string, token: string, deliveryAddress?: string) =>
    request<PocketView>("POST", `/p/${shortCode}/accept`, {
      token,
      body: { delivery_address: deliveryAddress ?? "" },
    }),

  cancel: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/cancel`, { token }),

  enterCode: (shortCode: string, token: string, code: string) =>
    request<CodeEntryResult>("POST", `/p/${shortCode}/enter-code`, { token, body: { code } }),

  reportIssue: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/report-issue`, { token }),

  confirmDispatchFailure: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/confirm-dispatch-failure`, { token }),

  attestNonReceipt: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/attest-non-receipt`, { token }),

  openDispute: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/dispute`, { token }),

  concede: (shortCode: string, token: string) =>
    request<PocketView>("POST", `/p/${shortCode}/concede`, { token }),

  getDispute: (shortCode: string, token: string) =>
    request<DisputeView>("GET", `/p/${shortCode}/dispute`, { token }),

  uploadEvidence: (shortCode: string, token: string, type: string, file: File) => {
    const form = new FormData();
    form.append("type", type);
    form.append("file", file);
    return request<EvidenceItem>("POST", `/p/${shortCode}/evidence`, { token, raw: form });
  },

  releaseCode: (pocketId: string, token: string) =>
    request<{ release_code: string }>("GET", `/pockets/${pocketId}/release-code`, { token }),

  simulateFunding: (pocketId: string) =>
    request<{ pocket_id: string; state: PocketState; status: string }>(
      "POST",
      `/demo/pockets/${pocketId}/simulate-funding`,
    ),

  adminDetail: (pocketId: string) =>
    request<AdminDetail>("GET", `/admin/pockets/${pocketId}`),

  adminDisputes: () =>
    request<{ disputes: DisputeQueueItem[] }>("GET", "/admin/disputes"),

  forceRefund: (pocketId: string) =>
    request<AdminDetail>("POST", `/admin/pockets/${pocketId}/force-refund`),

  forcePayout: (pocketId: string, badFaith: boolean) =>
    request<AdminDetail>("POST", `/admin/pockets/${pocketId}/force-payout`, {
      body: { bad_faith: badFaith },
    }),
};
