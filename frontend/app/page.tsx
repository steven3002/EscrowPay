"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api, type PocketSummary } from "@/lib/api";
import { listRecent, type RecentPocket } from "@/lib/recent";
import { useMe } from "@/lib/useMe";
import { AppHeader } from "@/components/AppHeader";
import { StateBadge } from "@/components/StateBadge";
import { Badge, Card, LinkButton, Page, SectionTitle } from "@/components/ui";

export default function Home() {
  const router = useRouter();
  const { user, known } = useMe();
  const [recent, setRecent] = useState<RecentPocket[]>([]);
  const [mine, setMine] = useState<PocketSummary[] | null>(null);
  const [link, setLink] = useState("");

  useEffect(() => {
    // Recent pockets live in localStorage, which is only readable after mount;
    // reading here (rather than in a state initializer) avoids a hydration
    // mismatch between server and client render.
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

  return (
    <Page>
      <AppHeader user={user} known={known} next="/" />
      <header className="mb-8 mt-2">
        <h1 className="text-2xl font-bold leading-tight">
          Get paid without getting scammed.
        </h1>
        <p className="mt-2 text-sm text-muted">
          The bank holds the money until the buyer confirms the handoff with a
          Release Code. No chargebacks, no &ldquo;I&rsquo;ll pay on delivery.&rdquo;
        </p>
      </header>

      <div className="mb-8 grid gap-3">
        <LinkButton href="/create" tone="primary">
          Create a pocket
        </LinkButton>
        <form onSubmit={openLink} className="grid gap-2">
          <input
            value={link}
            onChange={(e) => setLink(e.target.value)}
            placeholder="Paste a pocket link…"
            className="h-12 w-full rounded-xl border border-border bg-background px-3 text-base outline-none focus:border-emerald-500"
          />
        </form>
      </div>

      {user && activeMine.length > 0 && (
        <section className="mb-8">
          <div className="mb-2 flex items-center justify-between">
            <SectionTitle>Your active pockets</SectionTitle>
            <Link href="/dashboard" className="text-xs font-semibold underline">
              See all
            </Link>
          </div>
          <div className="grid gap-2">
            {activeMine.map((p) => (
              <Link key={`${p.short_code}-${p.role}`} href={`/p/${p.short_code}`}>
                <Card className="flex items-center justify-between !p-4">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{p.item.description}</div>
                    <div className="mt-0.5 text-xs text-muted">as {p.role}</div>
                  </div>
                  <StateBadge state={p.state} />
                </Card>
              </Link>
            ))}
          </div>
        </section>
      )}

      {!user && recent.length > 0 && (
        <section className="mb-8">
          <SectionTitle>Your pockets on this device</SectionTitle>
          <div className="grid gap-2">
            {recent.map((p) => (
              <Link key={`${p.shortCode}-${p.role}`} href={`/p/${p.shortCode}?t=${p.token}`}>
                <Card className="flex items-center justify-between !p-4">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{p.item || p.shortCode}</div>
                    <div className="mt-0.5 text-xs text-muted">as {p.role}</div>
                  </div>
                  <Badge tone="zinc">{p.shortCode}</Badge>
                </Card>
              </Link>
            ))}
          </div>
        </section>
      )}

      <footer className="mt-auto pt-8 text-center text-xs text-muted">
        <Link href="/admin" className="underline">
          Arbitration dashboard
        </Link>
        <p className="mt-2">Sandbox demo · funds are simulated through the bank gateway.</p>
      </footer>
    </Page>
  );
}
