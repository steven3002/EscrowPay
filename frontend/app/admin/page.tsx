"use client";

import Link from "next/link";
import { useCallback, useState } from "react";
import { api, ApiError, type DisputeQueueItem } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { usePolling } from "@/lib/usePolling";
import { Badge, Banner, Button, Card, EmptyState, Page, SectionTitle, Skeleton, Spinner } from "@/components/ui";
import { ScaleIcon } from "@/components/icons";

export default function AdminQueue() {
  const [items, setItems] = useState<DisputeQueueItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [locked, setLocked] = useState(false);
  const [signingIn, setSigningIn] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const res = await api.adminDisputes();
      setItems(res.disputes ?? []);
      setError(null);
      setLocked(false);
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
        setLocked(true);
        setError(null);
        return;
      }
      setError(e instanceof ApiError ? e.message : "Couldn't load the queue.");
    }
  }, []);

  usePolling(refresh, 5000);

  // The arbitration surface requires an admin session in every mode. In
  // sandbox, the desk can be entered with one tap through the demo login.
  async function enterDesk() {
    setSigningIn(true);
    try {
      await api.demoLogin("+2348090000009", "Desk Admin", true);
      await refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Arbitrator sign-in failed.");
    } finally {
      setSigningIn(false);
    }
  }

  if (locked) {
    return (
      <Page>
        <div className="mb-4 flex items-center justify-between">
          <Link href="/" className="text-sm text-muted">
            ← EscrowPay
          </Link>
          <span className="text-xs font-semibold uppercase tracking-wide text-muted">Arbitration</span>
        </div>
        <Card>
          <SectionTitle>Arbitrator access</SectionTitle>
          <p className="mb-4 text-sm text-muted">
            The dispute desk is restricted to arbitrator accounts.
          </p>
          {error && <div className="mb-3"><Banner tone="red">{error}</Banner></div>}
          <Button tone="primary" disabled={signingIn} onClick={enterDesk}>
            {signingIn ? <Spinner /> : "Enter the desk (sandbox)"}
          </Button>
        </Card>
      </Page>
    );
  }

  return (
    <Page>
      <div className="mb-4 flex items-center justify-between">
        <Link href="/" className="text-sm text-muted">
          ← EscrowPay
        </Link>
        <span className="text-xs font-semibold uppercase tracking-wide text-muted">Arbitration</span>
      </div>
      <h1 className="mb-1 text-3xl font-semibold tracking-tight">Dispute queue</h1>
      <p className="mb-6 text-sm text-muted">Open disputes awaiting a decision.</p>

      {error && <Banner tone="red">{error}</Banner>}
      {items === null && !error && (
        <div className="grid gap-2.5">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[76px] rounded-2xl" />
          ))}
        </div>
      )}
      {items && items.length === 0 && (
        <EmptyState icon={<ScaleIcon className="h-6 w-6" />} title="No open disputes">
          All clear. New disputes will appear here the moment they&rsquo;re raised.
        </EmptyState>
      )}
      <div className="grid gap-2.5">
        {items?.map((d) => (
          <Link key={d.pocket_id} href={`/admin/p/${d.pocket_id}`}>
            <Card className="!p-4">
              <div className="mb-2 flex items-center justify-between">
                <SectionTitle>{d.class.replace(/_/g, " ")}</SectionTitle>
                <Badge tone="red">{d.short_code}</Badge>
              </div>
              <div className="flex items-center justify-between text-sm">
                <span className="text-muted">opened by {d.opened_by}</span>
                <span className="text-muted">{formatDateTime(d.created_at)}</span>
              </div>
            </Card>
          </Link>
        ))}
      </div>
    </Page>
  );
}
