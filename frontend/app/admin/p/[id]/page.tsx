"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useState } from "react";
import { api, ApiError, type AdminDetail } from "@/lib/api";
import { formatDateTime, formatKobo } from "@/lib/format";
import { usePolling } from "@/lib/usePolling";
import { StateBadge } from "@/components/StateBadge";
import { Banner, Button, Card, Page, Row, SectionTitle, Spinner } from "@/components/ui";

export default function AdminPocket() {
  const { id } = useParams<{ id: string }>();
  const [detail, setDetail] = useState<AdminDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [action, setAction] = useState<string | null>(null);
  const [badFaith, setBadFaith] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setDetail(await api.adminDetail(id));
      setError(null);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't load this pocket.");
    }
  }, [id]);

  usePolling(refresh, 5000);

  async function forceRefund() {
    setAction("refund");
    try {
      setDetail(await api.forceRefund(id));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Action failed.");
    } finally {
      setAction(null);
    }
  }

  async function forcePayout() {
    setAction("payout");
    try {
      setDetail(await api.forcePayout(id, badFaith));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Action failed.");
    } finally {
      setAction(null);
    }
  }

  if (!detail) {
    return (
      <Page>
        <Back />
        {error ? <Banner tone="red">{error}</Banner> : (
          <div className="flex justify-center pt-10 text-muted"><Spinner /></div>
        )}
      </Page>
    );
  }

  const m = detail.money;
  return (
    <Page>
      <Back />
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-xl font-bold">{detail.item.description}</h1>
          <p className="font-mono text-xs text-muted">{detail.short_code}</p>
        </div>
        <StateBadge state={detail.state} />
      </div>

      {error && <div className="mb-4"><Banner tone="red">{error}</Banner></div>}

      <div className="grid gap-4">
        <Card>
          <SectionTitle>Ledger</SectionTitle>
          <Row label="Buyer pays" value={formatKobo(m.buyer_total_kobo)} />
          <Row label="Vendor allocation" value={formatKobo(m.amount_kobo)} />
          {(m.commission_kobo ?? 0) > 0 && (
            <Row label="Broker commission" value={formatKobo(m.commission_kobo)} />
          )}
          <Row label="Protection fee" value={formatKobo(m.premium_kobo)} />
          {detail.delivery_address && <Row label="Deliver to" value={detail.delivery_address} />}
        </Card>

        {detail.dispute && (
          <Card>
            <SectionTitle>Dispute</SectionTitle>
            <Row label="Class" value={detail.dispute.class.replace(/_/g, " ")} />
            <Row label="Opened by" value={detail.dispute.opened_by} />
            <Row label="Status" value={detail.dispute.resolution || detail.dispute.state} />
          </Card>
        )}

        <Card>
          <SectionTitle>Evidence</SectionTitle>
          {detail.evidence.length === 0 ? (
            <p className="text-sm text-muted">No evidence attached.</p>
          ) : (
            detail.evidence.map((ev) => (
              <Row
                key={ev.id}
                label={`${ev.party} · ${ev.type.replace(/_/g, " ")}`}
                value={
                  ev.within_window === undefined
                    ? formatDateTime(ev.captured_at)
                    : ev.within_window
                      ? "in window ✓"
                      : "late ✗"
                }
              />
            ))
          )}
        </Card>

        {detail.state === "DISPUTED" && (
          <Card>
            <SectionTitle>Arbitrate</SectionTitle>
            <div className="grid gap-3">
              <Button tone="danger" disabled={action !== null} onClick={forceRefund}>
                {action === "refund" ? <Spinner /> : "Force refund (flag vendor)"}
              </Button>
              <label className="flex items-center gap-2 text-sm text-muted">
                <input
                  type="checkbox"
                  checked={badFaith}
                  onChange={(e) => setBadFaith(e.target.checked)}
                  className="h-4 w-4"
                />
                Buyer acted in bad faith (strike)
              </label>
              <Button tone="neutral" disabled={action !== null} onClick={forcePayout}>
                {action === "payout" ? <Spinner /> : "Force payout to vendor"}
              </Button>
            </div>
          </Card>
        )}

        <Card>
          <SectionTitle>Timeline</SectionTitle>
          <ol className="grid gap-2">
            {detail.events.map((e) => (
              <li key={e.id} className="flex items-center justify-between gap-3 text-sm">
                <span className="font-medium">
                  {e.kind.replace(/_/g, " ")}
                  <span className="ml-2 text-xs text-muted">by {e.actor}</span>
                </span>
                <span className="text-xs text-muted">{formatDateTime(e.created_at)}</span>
              </li>
            ))}
          </ol>
        </Card>
      </div>
    </Page>
  );
}

function Back() {
  return (
    <Link href="/admin" className="mb-4 inline-block text-sm text-muted">
      ← Dispute queue
    </Link>
  );
}
