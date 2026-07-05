# API Reference

The backend is a JSON HTTP API served on `:8080`. The browser reaches it through
the frontend's same-origin proxy at `/api/*`, so in the running app every path
below is prefixed with the site origin (e.g. `http://localhost:3000/api/...`).

- [Authentication](#authentication)
- [Endpoints](#endpoints)
- [The pocket lifecycle (state machine)](#the-pocket-lifecycle-state-machine)
- [Error responses](#error-responses)
- [Payment webhooks](#payment-webhooks)

---

## Authentication

Two independent mechanisms, used together.

### Account sessions

An account is established by signing in, either through **Google OIDC** (when
configured) or a **sandbox demo login**. Sign-in creates a server-side session
and sets an `escrowpay_session` cookie (HttpOnly, `SameSite=Lax`). Only the
SHA-256 of the cookie token is stored, so a database-only compromise cannot mint
a valid cookie.

```
POST /api/auth/demo      { "phone": "+234...", "display_name": "Ada", "admin": false }
```

The demo login is available only in sandbox mode. Passing `"admin": true` mints
an admin session, which every `/api/admin/*` endpoint requires.

### Link tokens (invitations)

A pocket is shared as a link carrying a **role-scoped link token**. The token is
an *invitation only*: it renders the unclaimed seat's terms and authorizes the
**claim** that binds the seat to the caller's signed-in account. Once a seat is
claimed it answers exclusively to its owner's session — a leaked or forwarded
link is inert (anonymous → 401, a different account → 403), including for the
Release Code endpoint. One account may hold at most one role per pocket.

The token is sent in the `X-Link-Token` header (the share URL also carries it as
`?t=`). A signed-in participant acting on a seat they already own does not need
the token.

### Cross-site protection and rate limits

Mutating requests are guarded by the `SameSite=Lax` cookie plus an `Origin`-host
check. Per-client token-bucket rate limits apply: a generous global ceiling, a
tight budget on credential endpoints, and a moderate budget on state-changing
pocket calls. Exceeding a budget returns `429`.

---

## Endpoints

Legend — **Auth**: `public` (no auth), `session`, `invite` (link token or owning
session), `admin` (admin session).

### Auth & identity

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET`  | `/api/auth/providers` | public | Which sign-in methods this deployment offers |
| `GET`  | `/api/auth/me` | public | The current account, or anonymous |
| `POST` | `/api/auth/demo` | public¹ | Sandbox demo sign-in |
| `POST` | `/api/auth/logout` | session | Revoke the current session |
| `GET`  | `/api/auth/google/start` | public | Begin the Google OIDC flow |
| `GET`  | `/api/auth/google/callback` | public | OIDC redirect target |

¹ Sandbox mode only.

### Dashboard

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/api/me/pockets` | session | Every pocket the account participates in, role-scoped per row |
| `GET` | `/api/fees?goods_kobo=` | public | Protection Premium quote for a goods value → `{goods_kobo, premium_kobo, buyer_total_kobo}` |

### Pocket lifecycle

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/pockets` | session | Create a pocket (terms + structure); returns share links. The Protection Premium is computed server-side and any submitted value is ignored |
| `GET`  | `/api/p/{shortCode}` | invite | Role-scoped view of a pocket |
| `POST` | `/api/p/{shortCode}/claim` | session + invite | Bind the caller's account to the invited role |
| `POST` | `/api/p/{shortCode}/claim-broker` | session + vendor invite | Convert a buyer-created p2p pocket to brokered: caller becomes the broker with `{vendor_amount_kobo}`, receives a fresh vendor link to forward |
| `POST` | `/api/p/{shortCode}/accept` | invite | Accept terms (buyer supplies delivery address); may fund transition #1 |
| `POST` | `/api/p/{shortCode}/verify-funding` | invite | Ask the gateway whether the funding order is paid and credit it (#2) if so; idempotent |
| `POST` | `/api/p/{shortCode}/cancel` | invite | Cancel/refund per current state (#4 / #5) |

### Delivery & settlement

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET`  | `/api/pockets/{id}/release-code` | invite (buyer) | Reveal the buyer's Release Code plaintext |
| `POST` | `/api/p/{shortCode}/enter-code` | invite (vendor) | Enter the Release Code at handoff (#6 / #8) |
| `POST` | `/api/p/{shortCode}/report-issue` | invite (buyer) | Open a not-as-described dispute in the inspection window (#12) |
| `POST` | `/api/p/{shortCode}/confirm-dispatch-failure` | invite (vendor) | Vendor confirms delivery failed → refund (#9) |
| `POST` | `/api/p/{shortCode}/attest-non-receipt` | invite (buyer) | Buyer attests non-receipt after grace (#9) |

### Disputes & evidence

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/p/{shortCode}/dispute` | invite | Open a not-delivered dispute from a frozen pocket (#10) |
| `POST` | `/api/p/{shortCode}/concede` | invite (vendor) | Vendor concedes → refund (#13) |
| `POST` | `/api/p/{shortCode}/evidence` | invite | Upload dispute media (multipart; size-capped) |
| `GET`  | `/api/p/{shortCode}/dispute` | invite | Dispute status for this pocket |

### Admin (arbitration)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET`  | `/api/admin/disputes` | admin | Open-dispute queue |
| `GET`  | `/api/admin/pockets/{id}` | admin | Full pocket detail: ledger, dispute, evidence, timeline |
| `POST` | `/api/admin/pockets/{id}/force-refund` | admin | Resolve to the buyer; flag the vendor (#14) |
| `POST` | `/api/admin/pockets/{id}/force-payout` | admin | Resolve to the vendor; optional bad-faith strike (#15) |

### Funding & webhooks

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/demo/pockets/{id}/simulate-funding` | public² | Drive funding (#2) without a real payment |
| `POST` | `/api/webhooks/nomba` | signature | Ingest a signed bank payment notification |

² Available only while the sandbox funding shortcut is enabled (see the
runbook). On real rails it is off by default.

### Health

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | public | Liveness + database reachability (not under `/api`) |

---

## The pocket lifecycle (state machine)

Every state change is one database transaction and appends exactly one
audit-log row. The transition numbers are stable identifiers used throughout the
codebase.

| # | From → To | Trigger |
|---|---|---|
| 1  | → `CREATED` | Pocket created; every participant has accepted |
| 2  | `CREATED` → `FUNDED` | Funding confirmed; Release Code issued to the buyer |
| 3  | `CREATED` → `EXPIRED` | Funding window lapsed unpaid |
| 4  | `CREATED` → `CANCELLED` | Either party cancels before funding |
| 5  | `FUNDED` → `REFUNDED` | Vendor or mutual cancel after funding |
| 6  | `FUNDED` → `DELIVERED_PENDING` | Valid Release Code entered at handoff |
| 7  | `FUNDED` → `FROZEN` | Delivery deadline lapsed with no code |
| 8  | `FROZEN` → `DELIVERED_PENDING` | Valid code entered late |
| 9  | `FROZEN` → `REFUNDED` | Vendor confirms failure, or grace lapses with buyer attestation |
| 10 | `FROZEN` → `DISPUTED` | Parties disagree on delivery (not_delivered) |
| 11 | `DELIVERED_PENDING` → `SETTLED` | Inspection window elapsed with no dispute |
| 12 | `DELIVERED_PENDING` → `DISPUTED` | Buyer reports an issue in the window (not_as_described) |
| 13 | `DISPUTED` → `REFUNDED` | Vendor concedes |
| 14 | `DISPUTED` → `REFUNDED` | Admin force refund; vendor flagged for fraud |
| 15 | `DISPUTED` → `SETTLED` | Admin force payout; optional bad-faith strike on the buyer |

`SETTLED`, `REFUNDED`, `CANCELLED`, and `EXPIRED` are terminal. Two invariants
hold everywhere: **settlement to the vendor occurs only from
`DELIVERED_PENDING`**, and **no refund fires on a timer alone** — every refund
requires a vendor concession, a buyer attestation after grace, an admin action,
or a pre-dispatch cancel.

Clock-triggered transitions (#3, #7, #9, #11) are driven by a background
sweeper, not by request traffic.

Instant Mode is not a separate state: it sets a zero-length inspection window, so
a funded, code-entered pocket is immediately due for settlement.

---

## Error responses

Errors are `{ "error": "<message>" }` with a status that classifies the cause.
Messages are intentionally non-leaky.

| Status | Meaning |
|---|---|
| `400 Bad Request` | Malformed body or invalid pocket terms |
| `401 Unauthorized` | Sign-in required, or a link to a claimed seat you don't own |
| `403 Forbidden` | Authenticated but not permitted for this role; or a cross-origin mutation |
| `404 Not Found` | No such pocket/resource |
| `409 Conflict` | Illegal transition for the current state; already claimed/accepted; brokered buyer acting before the vendor; optimistic-lock race; Release Code not ready |
| `413 Payload Too Large` | Evidence upload exceeds the cap |
| `423 Locked` | Release Code entry locked after too many wrong attempts |
| `429 Too Many Requests` | Rate limit exceeded |
| `503 Service Unavailable` | A sign-in method that isn't configured on this deployment |
| `500 Internal Server Error` | Unexpected; logged in full server-side |

---

## Payment webhooks

`POST /api/webhooks/nomba` ingests signed payment notifications from the bank
gateway. The endpoint is mounted only when a webhook signature key is configured
(real-rails deployments).

**Authentication is the signature.** Each delivery carries an HMAC-SHA256
signature in the `nomba-signature` header (`nomba-sig-value` is accepted as a
fallback) computed over a colon-joined string of the event's identity fields and
the `nomba-timestamp` header. A delivery whose signature does not verify is
rejected (`401`) before anything is read from its body.

**Exactly-once processing.** A verified event is recorded under its provider
event id for replay protection, then processed idempotently:

- `payment_success` → maps the order reference to its pocket, guards that the
  amount is not below the buyer total, and drives transition #2
  (`CREATED → FUNDED`) through the same executor as every other write.
- `payout_success` / `payout_failed` → confirms or fails the matching
  settlement leg by its merchant reference.

Redeliveries and replays are no-ops. Events the current build cannot map
(unknown reference, underpayment, a pocket already closed) are acknowledged
(`200`) with the raw payload retained for operator review, but not marked
processed. The endpoint returns non-`2xx` only to request a provider retry.

**Pull-side confirmation.** The webhook is not the only way funding confirms.
`POST /api/p/{shortCode}/verify-funding` asks the gateway directly whether the
order was paid and, if so, credits it through the *same* idempotent path — so a
payment confirms even when no webhook is configured or a notification is lost.
A background sweep runs the same check over open pockets. Both routes and the
webhook converge on one funding-credit path, so a pocket funds at most once
however the confirmation arrives.
