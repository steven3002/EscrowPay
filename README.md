# EscrowPay

**Bank-native micro-escrow for social commerce.** EscrowPay turns a WhatsApp or
Instagram deal between two strangers into a protected transaction: the buyer's
money is held by a bank until the item is physically in hand, and the vendor is
paid the moment delivery is proven — not before.

It is a state machine over a bank's payment rails. **EscrowPay never touches the
money.** Funds sit in the bank's custody; the application decides when the bank
moves them, records every decision in an append-only log, and guarantees the
money moves exactly once.

---

## The problem

Social commerce in Nigeria runs on direct bank transfers between people who have
never met. Every deal is a leap of faith in one direction: pay first and hope
the vendor ships, or ship first and hope the buyer pays. Buyers get scammed by
vendors who vanish; vendors lose goods to buyers who charge back or cancel at
the door. There is no neutral party holding the money.

## The idea

A **Pocket** is a transaction-specific escrow position, funded by bank transfer
and shareable as a link in any chat. It has one lifecycle and exactly one
terminal outcome — settled to the vendor, refunded to the buyer, or split by
arbitration.

Two mechanisms make the guarantee real:

- **Possession-gated release.** Funding generates a 4-digit **Release Code**
  shown only on the buyer's screen. The vendor is paid only when that code is
  entered at handoff — *no code, no payment*. The code is the buyer's proof of
  possession, not an approval button, and a permanent on-screen shield warns the
  buyer never to read it out remotely.
- **Evidence-gated settlement.** In quality-protection mode, settlement waits out
  an inspection window during which the buyer can open a dispute backed by an
  in-app unboxing video. Disputes are decided against canonical, tamper-frozen
  terms, with defined burdens of proof for each side.

No refund ever fires on a timer alone: every refund requires a vendor
concession, a buyer attestation after a grace period, an admin action, or a
pre-dispatch cancellation.

## Modes and shapes

- **Instant Mode** — delivery-only protection (zero inspection window). Still
  strictly better than a naked transfer: the vendor cannot be paid unless
  *something was handed over* at the agreed address.
- **Cooldown Mode** — adds a quality-inspection window with unboxing-video
  dispute rights.
- **Two-party (p2p)** — buyer ↔ vendor.
- **Broker Mode (three-party)** — a broker resells a supplier's item under
  canonical terms with a commission leg. The buyer's experience is identical to
  p2p (their counterparty of record is the broker's storefront); the platform
  settles the vendor and the broker as separate legs. Money visibility is
  double-blind between parties and fully transparent to the platform and bank.

---

## Architecture

```
   Buyer / Vendor / Broker
   (WhatsApp · Instagram · link)
              │
              ▼
   ┌──────────────────────┐        same-origin /api proxy
   │  Next.js PWA          │  ───────────────────────────┐
   │  (installable, mobile)│                              │
   └──────────────────────┘                              ▼
                                          ┌────────────────────────────┐
                                          │  Go API (net/http)          │
                                          │  ├─ httpapi   transport     │
                                          │  ├─ auth      sessions/OIDC │
                                          │  ├─ pocketapp use cases      │
                                          │  ├─ pocket    domain core    │
                                          │  ├─ store     single write   │
                                          │  │             path (pgx)    │
                                          │  ├─ settlement sweeper        │
                                          │  └─ gateway   payment boundary│
                                          └───────┬───────────────┬──────┘
                                                  │               │
                                          ┌───────▼──────┐   ┌────▼─────────┐
                                          │ PostgreSQL   │   │ Bank gateway │
                                          │ (source of   │   │ (funding +   │
                                          │  truth)      │   │  payouts)    │
                                          └──────────────┘   └──────────────┘
```

**Design spine — the single write path.** Every state change runs through one
transactional executor: `SELECT … FOR UPDATE` on the pocket row → a pure
domain-logic guard → state write + exactly one append-only event row → commit.
Money-moving transitions write their settlement legs *inside the same
transaction*; disbursement happens afterward, idempotently, and a background
sweeper reconciles anything a crash left unfinished. The result is a system
where a race can never double-pay, double-refund, or silently overwrite.

**The domain core is pure.** The `pocket` package — the entire state machine and
all its guards — imports no database, HTTP, or payment code. It takes a snapshot
and an event and returns the next snapshot plus a list of effects. Everything
else is wiring around that core.

### Backend packages (`backend/internal`)

| Package | Responsibility |
|---|---|
| `pocket` | Pure domain core: states, the #1–#15 transition table, guards, effects |
| `releasecode` | Release Code generation, HMAC verifier, encrypted buyer-retrievable copy, attempt/lockout policy |
| `store` | pgx repositories and the single transactional write path; embedded SQL migrations |
| `pocketapp` | Application layer: use cases and the effects executor (gateway/notify/settlement) |
| `httpapi` | HTTP transport: routing, authentication, role-scoped serialization, rate limiting, webhook ingestion |
| `auth` | Server-side sessions and Google OIDC sign-in |
| `linktoken` | Role-scoped invitation tokens for sharing a pocket seat |
| `gateway` | Payment provider boundary; `mock` (default) and `nomba` (real) implementations |
| `settlement` | Interval sweeper driving every clock-triggered transition and leg reconciliation |
| `evidence` | Dispute-media storage |
| `notify` | Notifier interface with a structured-log implementation |
| `ratelimit` | In-process per-client token buckets |

### Stack

Go (standard-library HTTP, `pgx`, `goose` migrations, `int64` kobo) · PostgreSQL
16 · Next.js (App Router, TypeScript, Tailwind) PWA · Docker for local Postgres.

---

## Quickstart

Prerequisites: Go 1.22+, Node 20+, and Docker. Three terminals.

```bash
# 1. Postgres (see docs/RUNBOOK.md for the compose file / a docker run one-liner)
docker compose up -d

# 2. Backend — migrates on boot, serves on :8080
cd backend
go run ./cmd/api

# 3. Frontend — proxies /api to the backend, serves on :3000
cd frontend
npm install
npm run dev
```

Open <http://localhost:3000>. Out of the box the app runs on the **mock payment
gateway** (no real money, no credentials needed): the buyer funds a pocket with
a one-click sandbox button. To run against real bank rails, see
[docs/RUNBOOK.md](docs/RUNBOOK.md).

A guided walk-through of the full lifecycle — create → accept → fund → deliver →
settle — is in [docs/DEMO.md](docs/DEMO.md).

## Configuration

The backend is configured entirely through environment variables (with
development-safe defaults for everything except production secrets). See
[`backend/.env.example`](backend/.env.example) for the full annotated surface
and [docs/RUNBOOK.md](docs/RUNBOOK.md#configuration-reference) for the reference
table. The API also loads a `backend/.env` file on boot if present (real
environment variables take precedence).

## Documentation

| Document | Contents |
|---|---|
| [docs/API.md](docs/API.md) | Endpoint reference, authentication model, state machine, error codes, webhook contract |
| [docs/RUNBOOK.md](docs/RUNBOOK.md) | Local setup, configuration reference, mock↔bank toggle, real-rails setup, known limitations, reset procedure |
| [docs/DEMO.md](docs/DEMO.md) | The scripted end-to-end demo with two-device choreography and a fallback path |

## Testing

```bash
cd backend
go test ./...              # full suite (integration tests need Postgres on :5433)
go test ./... -race        # race detector

cd ../frontend
npm run build && npm run lint
```

The backend suite covers the domain state machine, the transactional write path,
the HTTP surface end-to-end, exactly-once settlement under forced retries and
crashes, dispute and arbitration flows, three-party Broker Mode, session and
rate-limit enforcement, and signed webhook ingestion (valid, forged, and
replayed).

## Project layout

```
backend/     Go API, domain core, persistence, payment gateway
frontend/    Next.js PWA
docs/        API reference, runbook, demo script
```

## Status

A complete, working MVP. Payment rails run against a bank sandbox; identity,
disputes, arbitration, Broker Mode, and the full lifecycle are implemented and
tested. Real WhatsApp/SMS notifications and production KYC linkage are the main
post-MVP items.
