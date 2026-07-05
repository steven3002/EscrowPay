"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { api, type PocketSummary } from "@/lib/api";
import { listRecent, type RecentPocket } from "@/lib/recent";
import { useMe } from "@/lib/useMe";
import { StateBadge } from "@/components/StateBadge";
import { Badge, ListRow } from "@/components/ui";
import { LinkIcon, LockIcon, PlusIcon, ScaleIcon, ShieldIcon } from "@/components/icons";

// Deal-shape shortcuts shown as suggestion chips under the hero — a nod to the
// reference layout, and a compact way to show the product spans every shape of
// social-commerce deal. Each starts the create flow.
const CHIPS = [
  { label: "Sell an item safely", href: "/create?as=vendor" },
  { label: "Buy from a vendor", href: "/create?as=buyer" },
  { label: "Broker a resale", href: "/create?type=brokered" },
];

export default function Home() {
  const router = useRouter();
  const { user } = useMe();
  const [recent, setRecent] = useState<RecentPocket[]>([]);
  const [mine, setMine] = useState<PocketSummary[] | null>(null);
  const [link, setLink] = useState("");

  useEffect(() => {
    // Recent pockets live in localStorage, which is only readable after mount;
    // reading here avoids a server/client hydration mismatch.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRecent(listRecent());
  }, []);

  const loadMine = useCallback(async () => {
    try {
      const res = await api.myPockets();
      setMine(res.pockets ?? []);
    } catch {
      setMine(null);
    }
  }, []);
  useEffect(() => {
    if (user) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- async fetch: state lands after the awaited response
      loadMine();
    }
  }, [user, loadMine]);

  function openLink(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = link.trim();
    if (!trimmed) return;
    // Accept a full share URL or a bare "/p/<code>?t=<token>" path.
    try {
      const url = trimmed.startsWith("http") ? new URL(trimmed) : new URL(trimmed, window.location.origin);
      router.push(url.pathname + url.search);
    } catch {
      router.push(trimmed.startsWith("/") ? trimmed : `/${trimmed}`);
    }
  }

  const activeMine = (mine ?? []).filter((p) => p.active).slice(0, 4);
  const firstName = user?.display_name?.trim().split(/\s+/)[0];

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col px-4 pb-28 pt-8 md:px-8 md:pb-16 md:pt-16">
      {/* Hero */}
      <header className="text-center">
        {firstName ? (
          <p className="text-sm font-semibold text-accent">Welcome back, {firstName} 👋</p>
        ) : (
          <span className="inline-flex items-center gap-2 rounded-full border border-border bg-surface px-3 py-1 text-xs font-semibold text-muted shadow-[var(--shadow-card)]">
            <span className="h-1.5 w-1.5 rounded-full bg-accent" />
            Bank-native escrow for social commerce
          </span>
        )}
        <h1 className="mx-auto mt-4 max-w-2xl text-balance text-4xl font-semibold leading-[1.08] tracking-tight md:text-5xl">
          Turn any chat into a protected deal.
        </h1>
        <p className="mx-auto mt-4 max-w-xl text-pretty text-base text-muted md:text-lg">
          EscrowPay keeps the buyer&rsquo;s money in the bank until the item is in hand, then pays the
          vendor the moment delivery is proven. No chargebacks, no &ldquo;pay me first,&rdquo; no
          disappearing acts &mdash; just a link you drop into any DM.
        </p>
      </header>

      {/* Suggestion chips */}
      <div className="mt-7 flex flex-wrap justify-center gap-2.5">
        {CHIPS.map((c) => (
          <Link
            key={c.label}
            href={c.href}
            className="rounded-full bg-surface-muted px-4 py-2 text-sm font-medium text-foreground transition-colors hover:bg-border"
          >
            {c.label}
          </Link>
        ))}
      </div>

      {/* Action card — the hero centerpiece: create a pocket or open a shared link. */}
      <div className="mx-auto mt-5 w-full max-w-2xl">
        <form
          onSubmit={openLink}
          className="rounded-3xl border border-border bg-surface p-2.5 shadow-[var(--shadow-card)] focus-within:border-accent/50"
        >
          <div className="flex items-center gap-2.5 px-3 pt-2.5">
            <LinkIcon className="h-5 w-5 shrink-0 text-muted" />
            <input
              value={link}
              onChange={(e) => setLink(e.target.value)}
              placeholder="Paste a pocket link to open it…"
              aria-label="Paste a pocket link"
              className="h-10 flex-1 bg-transparent text-base outline-none placeholder:text-muted"
            />
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 border-t border-border/70 px-2 pt-2.5">
            <span className="ml-1 hidden text-xs text-muted sm:mr-auto sm:block">
              Received a link? Paste it above. Starting a new deal? Create a pocket.
            </span>
            <button
              type="submit"
              className="ml-auto inline-flex h-10 items-center rounded-full border border-border px-4 text-sm font-semibold text-foreground transition-colors hover:bg-surface-muted sm:ml-0"
            >
              Open link
            </button>
            <Link
              href="/create"
              className="inline-flex h-10 items-center gap-2 rounded-full bg-accent px-4 text-sm font-semibold text-white transition-colors hover:bg-accent-strong"
            >
              <PlusIcon className="h-[18px] w-[18px]" />
              Create a pocket
            </Link>
          </div>
        </form>
      </div>

      {/* Your pockets (signed in) */}
      {user && activeMine.length > 0 && (
        <section className="mx-auto mt-10 w-full max-w-2xl">
          <div className="mb-2 flex items-center justify-between">
            <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">Your active pockets</h2>
            <Link href="/dashboard" className="text-xs font-semibold text-accent hover:underline">
              See all
            </Link>
          </div>
          <div className="grid gap-2.5">
            {activeMine.map((p) => (
              <ListRow
                key={`${p.short_code}-${p.role}`}
                href={`/p/${p.short_code}`}
                icon={<ShieldIcon className="h-5 w-5" />}
                title={p.item.description}
                subtitle={`as ${p.role}`}
                trailing={<StateBadge state={p.state} />}
              />
            ))}
          </div>
        </section>
      )}

      {/* Recent on this device (anonymous) */}
      {!user && recent.length > 0 && (
        <section className="mx-auto mt-10 w-full max-w-2xl">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">
            Your pockets on this device
          </h2>
          <div className="grid gap-2.5">
            {recent.map((p) => (
              <ListRow
                key={`${p.shortCode}-${p.role}`}
                href={`/p/${p.shortCode}?t=${p.token}`}
                icon={<ShieldIcon className="h-5 w-5" />}
                title={p.item || p.shortCode}
                subtitle={`as ${p.role}`}
                trailing={<Badge tone="zinc">{p.shortCode}</Badge>}
              />
            ))}
          </div>
        </section>
      )}

      {/* How the guarantee works */}
      <section className="mt-14 md:mt-20">
        <h2 className="text-center text-xs font-semibold uppercase tracking-wide text-muted">
          Why both sides are safe
        </h2>
        <div className="mt-4 grid gap-3 md:grid-cols-3">
          <ValueCard
            icon={<ShieldIcon className="h-5 w-5" />}
            title="The bank is the vault"
            body="EscrowPay never touches the money. Funds sit in the sponsor bank's custody until the protocol says to move them — exactly once."
          />
          <ValueCard
            icon={<LockIcon className="h-5 w-5" />}
            title="No code, no payment"
            body="A 4-digit Release Code is shown only to the buyer and exchanged for the package at handoff. The vendor is paid only when it's entered."
          />
          <ValueCard
            icon={<ScaleIcon className="h-5 w-5" />}
            title="Fraud made unprofitable"
            body="Disputes are settled by evidence — an in-app unboxing video, proof of dispatch — against tamper-frozen terms. The losing side bears the loss."
          />
        </div>
      </section>

      {/* Range — depicts the product's breadth */}
      <section className="mt-10 rounded-3xl border border-border bg-surface p-6 shadow-[var(--shadow-card)] md:p-8">
        <div className="grid items-center gap-6 md:grid-cols-[1.2fr_1fr]">
          <div>
            <h2 className="text-xl font-semibold tracking-tight md:text-2xl">One protocol, every shape of deal.</h2>
            <p className="mt-2 text-sm text-muted md:text-base">
              Two people, or a broker reselling a supplier&rsquo;s goods on commission. Delivery-only
              protection, or a full quality-inspection window with dispute rights. From an
              &#8358;8,000 Instagram order to a brokered dropship &mdash; the same guarantee, on
              rails the bank already trusts.
            </p>
          </div>
          <div className="flex flex-wrap gap-2 md:justify-end">
            {["Buyer ↔ Vendor", "Broker (3-party)", "Instant mode", "Cooldown mode", "Arbitrated"].map(
              (t) => (
                <span
                  key={t}
                  className="rounded-full border border-border bg-surface-muted px-3 py-1.5 text-xs font-semibold text-foreground"
                >
                  {t}
                </span>
              ),
            )}
          </div>
        </div>
      </section>

      <footer className="mt-auto pt-12 text-center text-xs text-muted">
        <p>Sandbox demo · funds are simulated through the bank gateway.</p>
      </footer>
    </div>
  );
}

function ValueCard({ icon, title, body }: { icon: ReactNode; title: string; body: string }) {
  return (
    <div className="rounded-2xl border border-border bg-surface p-5 shadow-[var(--shadow-card)]">
      <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-accent-soft text-accent">
        {icon}
      </span>
      <h3 className="mt-3.5 text-sm font-semibold">{title}</h3>
      <p className="mt-1.5 text-sm leading-relaxed text-muted">{body}</p>
    </div>
  );
}
