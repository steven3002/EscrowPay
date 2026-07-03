"use client";

import Link from "next/link";
import { useCallback, useState } from "react";
import { api, ApiError, type PocketSummary, type Role } from "@/lib/api";
import { formatKobo, stateTone, countdown } from "@/lib/format";
import { useMe } from "@/lib/useMe";
import { usePolling } from "@/lib/usePolling";
import { AppHeader } from "@/components/AppHeader";
import { StateBadge } from "@/components/StateBadge";
import { Badge, Banner, Card, LinkButton, Page, SectionTitle, Spinner } from "@/components/ui";

const ROLE_TONE: Record<Role, string> = { buyer: "blue", vendor: "emerald", broker: "amber" };

// Dashboard is the account's cross-role overview: every pocket they take part
// in — as buyer, vendor, or broker — split into the ones still moving and the
// ones that have closed.
export default function Dashboard() {
  const { user, known } = useMe();
  const [pockets, setPockets] = useState<PocketSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [roleFilter, setRoleFilter] = useState<Role | "all">("all");

  const refresh = useCallback(async () => {
    try {
      const res = await api.myPockets();
      setPockets(res.pockets ?? []);
      setError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setPockets(null);
        setError(null);
        return;
      }
      setError(e instanceof ApiError ? e.message : "Couldn't load your pockets.");
    }
  }, []);
  usePolling(refresh, 5000);

  if (known && !user) {
    return (
      <Page>
        <AppHeader user={user} known={known} next="/dashboard" />
        <Card>
          <SectionTitle>Your pockets</SectionTitle>
          <p className="mb-4 text-sm text-muted">Sign in to see every deal you take part in.</p>
          <LinkButton href="/login?next=/dashboard" tone="primary">
            Sign in
          </LinkButton>
        </Card>
      </Page>
    );
  }

  const filtered = (pockets ?? []).filter((p) => roleFilter === "all" || p.role === roleFilter);
  const active = filtered.filter((p) => p.active);
  const ended = filtered.filter((p) => !p.active);

  return (
    <Page>
      <AppHeader user={user} known={known} next="/dashboard" />
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-bold">Your pockets</h1>
        <Link href="/create" className="text-sm font-semibold text-emerald-700 underline dark:text-emerald-300">
          + New
        </Link>
      </div>

      <div className="mb-4 flex gap-2">
        {(["all", "buyer", "vendor", "broker"] as const).map((r) => (
          <button
            key={r}
            onClick={() => setRoleFilter(r)}
            className={`rounded-full px-3 py-1.5 text-xs font-semibold capitalize transition-colors ${
              roleFilter === r
                ? "bg-emerald-600 text-white"
                : "border border-border text-muted hover:bg-black/5 dark:hover:bg-white/5"
            }`}
          >
            {r}
          </button>
        ))}
      </div>

      {error && <div className="mb-4"><Banner tone="red">{error}</Banner></div>}
      {pockets === null && !error && (
        <div className="flex justify-center pt-10 text-muted">
          <Spinner />
        </div>
      )}
      {pockets !== null && filtered.length === 0 && (
        <Card>
          <p className="mb-4 text-sm text-muted">
            Nothing here yet. Create a pocket or open a link someone shared with you.
          </p>
          <LinkButton href="/create" tone="primary">
            Create a pocket
          </LinkButton>
        </Card>
      )}

      {active.length > 0 && (
        <section className="mb-6">
          <SectionTitle>Active</SectionTitle>
          <div className="grid gap-2">
            {active.map((p) => (
              <PocketCard key={`${p.short_code}-${p.role}`} p={p} />
            ))}
          </div>
        </section>
      )}
      {ended.length > 0 && (
        <section>
          <SectionTitle>Ended</SectionTitle>
          <div className="grid gap-2">
            {ended.map((p) => (
              <PocketCard key={`${p.short_code}-${p.role}`} p={p} />
            ))}
          </div>
        </section>
      )}
    </Page>
  );
}

// amountFor renders the one figure this role is entitled to see.
function amountFor(p: PocketSummary): string {
  if (p.role === "vendor") return formatKobo(p.money.amount_kobo);
  if (p.role === "broker") return `${formatKobo(p.money.commission_kobo)} commission`;
  return formatKobo(p.money.buyer_total_kobo);
}

// nextDeadline picks the timer that matters in the pocket's current state.
function nextDeadline(p: PocketSummary): string | null {
  const t = p.timers;
  const pick =
    p.state === "CREATED"
      ? { label: "funding closes", at: t.funding_expires_at }
      : p.state === "FUNDED"
        ? { label: "deliver by", at: t.delivery_deadline }
        : p.state === "DELIVERED_PENDING"
          ? { label: "settles", at: t.settle_after }
          : p.state === "FROZEN"
            ? { label: "grace ends", at: t.grace_deadline }
            : null;
  if (!pick?.at) return null;
  const c = countdown(pick.at, Date.now());
  return c.lapsed ? `${pick.label} any moment` : `${pick.label} in ${c.text}`;
}

function PocketCard({ p }: { p: PocketSummary }) {
  const deadline = nextDeadline(p);
  return (
    <Link href={`/p/${p.short_code}`}>
      <Card className={`!p-4 ${p.active ? "" : "opacity-70"}`}>
        <div className="mb-1.5 flex items-center justify-between gap-2">
          <div className="truncate text-sm font-semibold">{p.item.description}</div>
          <StateBadge state={p.state} />
        </div>
        <div className="flex items-center justify-between gap-2 text-xs">
          <span className="flex items-center gap-2">
            <Badge tone={ROLE_TONE[p.role]}>{p.role}</Badge>
            {p.structure === "brokered" && <Badge tone="zinc">brokered</Badge>}
            <span className="text-muted">{amountFor(p)}</span>
          </span>
          {deadline && <span className={`text-muted ${stateTone(p.state) === "red" ? "text-red-500" : ""}`}>{deadline}</span>}
        </div>
      </Card>
    </Link>
  );
}
