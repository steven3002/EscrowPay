# Runbook

Everything needed to run EscrowPay on a clean machine, configure it, switch
between the mock and real bank gateways, and reset it between demo runs.

- [Prerequisites](#prerequisites)
- [Start the stack](#start-the-stack)
- [Configuration reference](#configuration-reference)
- [Payment gateway: mock vs. bank rails](#payment-gateway-mock-vs-bank-rails)
- [Reset between demo runs](#reset-between-demo-runs)
- [Known limitations](#known-limitations)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

- **Go 1.22+**
- **Node 20+**
- **Docker** (for PostgreSQL)

No system PostgreSQL client is required.

---

## Start the stack

Three processes: a database, the API, and the web app.

### 1. PostgreSQL

The project runs Postgres in Docker on host port **5433** (chosen to avoid a
conflict with any default `5432` instance). The repository provides a
`docker-compose.yml` at its root:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: escrowpay
      POSTGRES_PASSWORD: escrowpay_dev
      POSTGRES_DB: escrowpay
    ports:
      - "5433:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U escrowpay -d escrowpay"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
```

```bash
docker compose up -d
```

> If `docker-compose.yml` is not present after cloning, save the snippet above
> to the repo root first. Alternatively, run Postgres directly:
>
> ```bash
> docker run -d --name escrowpay-pg -p 5433:5432 \
>   -e POSTGRES_USER=escrowpay -e POSTGRES_PASSWORD=escrowpay_dev \
>   -e POSTGRES_DB=escrowpay postgres:16-alpine
> ```

### 2. Backend

```bash
cd backend
go run ./cmd/api
```

The API applies database migrations on boot and serves on `:8080`. Confirm it is
healthy:

```bash
curl localhost:8080/healthz     # {"database":"ok","status":"ok"}
```

### 3. Frontend

```bash
cd frontend
npm install
npm run dev
```

The web app serves on `:3000` and proxies `/api/*` to the backend (override the
target with `BACKEND_ORIGIN`). Open <http://localhost:3000>.

---

## Configuration reference

The backend reads its configuration from environment variables and, if present,
a `backend/.env` file (real environment variables win). See
[`backend/.env.example`](../backend/.env.example) for a copyable template.

### Core

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | `postgres://escrowpay:escrowpay_dev@localhost:5433/escrowpay` | Postgres connection string |
| `LISTEN_ADDR` | `:8080` | API listen address |
| `LINK_TOKEN_SECRET` | *(dev fallback)* | HMAC key for link tokens — set in production |
| `RELEASE_CODE_SECRET` | *(dev fallback)* | Key for the Release Code verifier and encryption — set in production |
| `SANDBOX_MODE` | `true` | Enables the demo login |

### Policy & operations

| Variable | Default | Purpose |
|---|---|---|
| `FUNDING_LINK_TTL_HOURS` | `72` | Funding window before a pocket expires |
| `GRACE_HOURS` | `24` | Grace period on a frozen pocket before attested refund |
| `EVIDENCE_CAPTURE_WINDOW_MINUTES` | `60` | Unboxing-video capture window after handoff |
| `SWEEPER_ENABLED` | `true` | Run the clock-triggered transition sweeper |
| `SWEEPER_INTERVAL_SECONDS` | `60` | Sweeper poll interval |
| `EVIDENCE_DIR` | `./data/evidence` | Local dispute-media directory |
| `EVIDENCE_MAX_MB` | `25` | Per-upload size cap |

### Sessions & hardening

| Variable | Default | Purpose |
|---|---|---|
| `SESSION_TTL_HOURS` | `720` | Session lifetime |
| `COOKIE_SECURE` | `false` | Mark cookies `Secure` (set `true` behind HTTPS) |
| `TRUST_PROXY` | `true` | Key rate limits on `X-Forwarded-For` |
| `RATE_LIMIT_ENABLED` | `true` | Enable per-client rate limits |
| `GOOGLE_CLIENT_ID` / `_SECRET` / `_REDIRECT_URL` | — | Google sign-in (enabled only when all three set) |

### Payment gateway

| Variable | Default | Purpose |
|---|---|---|
| `GATEWAY_PROVIDER` | `mock` | `mock` or `nomba` |
| `SIMULATE_FUNDING_ENABLED` | `mock` only | Keep the one-click funding shortcut available |
| `NOMBA_BASE_URL` | `https://sandbox.nomba.com` | `sandbox` or `https://api.nomba.com` |
| `NOMBA_CLIENT_ID` / `NOMBA_CLIENT_SECRET` | — | API credentials (fallbacks: `TEST_CREDENTIALS_CLIENT_ID` / `_SECRET`) |
| `NOMBA_PARENT_ACCOUNT_ID` | — | Parent account id, sent on every call (fallback: `PARENT_ACCOUNT_ID`) |
| `NOMBA_SUB_ACCOUNT_ID` | — | Sub-account for funds and transfers (fallback: `SUB_ACCOUNT_ID`) |
| `NOMBA_SIGNATURE_KEY` | — | Webhook HMAC key; unset leaves the webhook endpoint unmounted |
| `PUBLIC_BASE_URL` | `http://localhost:3000` | Origin the hosted checkout redirects back to |
| `NOMBA_FALLBACK_CUSTOMER_EMAIL` | *(built-in)* | Receipt email when a buyer has none |
| `NOMBA_PAYOUT_*` / `NOMBA_REFUND_*` | — | Default transfer beneficiary (account number, bank code, name) |

---

## Payment gateway: mock vs. bank rails

### Mock (default)

With `GATEWAY_PROVIDER` unset or `mock`, no money moves. Funding links are
fabricated and the buyer funds a pocket with a one-click **sandbox** button
(`simulate-funding`). This is the zero-configuration path — nothing below is
needed — and is the recommended way to demo the full lifecycle.

### Nomba bank rails

Set `GATEWAY_PROVIDER=nomba` and provide the credentials. The adapter
authenticates with the parent account id on every call and scopes money
operations to the sub-account; funding is a hosted checkout order, and
payouts/refunds are bank transfers.

1. **Credentials.** Put the sandbox `client id`, `client secret`, parent account
   id, and sub-account id in `backend/.env` (the dashboard export names are
   accepted directly).
2. **Default beneficiary.** Set `NOMBA_PAYOUT_*` (and optionally `NOMBA_REFUND_*`)
   to a valid destination account so settlement transfers have somewhere to go.
3. **Webhook.** Funding confirms only via the signed `payment_success` webhook,
   so the API must be reachable by the bank:
   - Expose `POST /api/webhooks/nomba` at a public URL (deploy, or tunnel with a
     tool such as ngrok/cloudflared).
   - In the bank dashboard → Developer → Webhook Setup, set that URL for the test
     environment and copy the signature key into `NOMBA_SIGNATURE_KEY`.
4. **Fallback.** Set `SIMULATE_FUNDING_ENABLED=true` to keep the one-click
   funding shortcut available on rails, in case the sandbox checkout misbehaves
   during a live demo.

> **Sandbox truth model.** On the bank sandbox, transaction-lookup and balance
> endpoints return canned fixtures for any query, so polling-based verification
> is intentionally off — the signed webhook is the single source of truth.

---

## Reset between demo runs

Clear all application data (accounts, pockets, events, settlements) while keeping
the schema. Using the compose service name works regardless of the container's
prefix:

```bash
docker compose exec -T postgres psql -U escrowpay -d escrowpay -c \
  "TRUNCATE sessions, settlements, evidence, disputes, pocket_events, \
   pocket_participants, pockets, users, webhook_events RESTART IDENTITY CASCADE;"
```

The backend does not need restarting after a reset. For a full teardown instead,
`docker compose down -v` removes the database volume; the next backend boot
re-migrates from empty.

---

## Known limitations

These are deliberate MVP boundaries, not defects:

- **Notifications are a log stub.** Every notification is written as a structured
  log line; real WhatsApp/SMS delivery is post-MVP.
- **Single currency and single bank.** Amounts are NGN kobo; one sponsor-bank
  gateway is assumed.
- **KYC/BVN is placeholder.** Identity anchoring fields exist but are not wired to
  a real verification provider.
- **Trust-tier has no earning logic** yet — it is a stored field.
- **Refunds use the transfer rail.** Funds return to a configured beneficiary
  account immediately; refunding the original payment instrument on the card
  rails is a production item (the funding transaction id is already captured).
- **Google sign-in needs real OAuth credentials.** Until they are set, the login
  surface offers only the sandbox demo login.

---

## Troubleshooting

**A code change didn't take effect.** A stale API process may still hold `:8080`.
Find and stop the listener:

```bash
ss -ltnp | grep :8080      # note the PID
kill <pid>
```

(With `go run`, the compiled binary lives in a temp directory, so matching by
`cmd/api` alone can miss it — match on the port instead.)

**`healthz` reports the database unreachable.** Confirm the container is up
(`docker compose ps`) and that nothing else holds `5433`.

**Frontend calls 404 or can't reach the API.** The web app expects the backend on
`http://localhost:8080`; set `BACKEND_ORIGIN` if it runs elsewhere.

**On rails, a payment never funds the pocket.** Funding depends on the webhook.
Verify `NOMBA_SIGNATURE_KEY` is set (the endpoint is unmounted without it) and
that the dashboard webhook URL reaches this instance. As a live-demo fallback,
enable `SIMULATE_FUNDING_ENABLED`.
