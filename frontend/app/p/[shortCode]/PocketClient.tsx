"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import {
  api,
  ApiError,
  type CodeEntryResult,
  type DisputeView,
  type PocketView,
} from "@/lib/api";
import { formatKobo } from "@/lib/format";
import { remember } from "@/lib/recent";
import { usePolling } from "@/lib/usePolling";
import { StateBadge } from "@/components/StateBadge";
import { Countdown } from "@/components/Countdown";
import {
  Banner,
  Button,
  Card,
  Field,
  Input,
  Page,
  Row,
  SectionTitle,
  Spinner,
} from "@/components/ui";

// PocketClient is the one screen every participant uses. It renders strictly
// from the server's role-scoped view and refetches on an interval and on focus,
// so the pocket always reflects server truth; the available actions are a pure
// function of (role, state). It holds no lifecycle logic of its own.
export default function PocketClient({ shortCode, token }: { shortCode: string; token: string }) {
  const [pocket, setPocket] = useState<PocketView | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    if (!token) {
      setError({ status: 401, message: "This link is missing its access token." });
      setLoading(false);
      return;
    }
    try {
      const p = await api.getPocket(shortCode, token);
      setPocket(p);
      setError(null);
    } catch (e) {
      setError(e instanceof ApiError ? { status: e.status, message: e.message } : { status: 0, message: "Network error" });
    } finally {
      setLoading(false);
    }
  }, [shortCode, token]);

  usePolling(refresh, 4000);

  useEffect(() => {
    if (pocket) {
      remember({ shortCode, pocketId: pocket.id, role: pocket.your_role, token, item: pocket.item.description });
    }
  }, [pocket, shortCode, token]);

  if (loading) {
    return (
      <Page>
        <div className="flex flex-1 items-center justify-center pt-24 text-muted">
          <Spinner />
        </div>
      </Page>
    );
  }

  if (error || !pocket) {
    const msg =
      error?.status === 404
        ? "This pocket doesn't exist."
        : error?.status === 403
          ? "This link isn't valid for this pocket."
          : error?.status === 401
            ? "This link is missing or has an invalid access token."
            : error?.message ?? "Couldn't load this pocket.";
    return (
      <Page>
        <TopBar shortCode={shortCode} />
        <Card className="mt-6">
          <Banner tone="red">{msg}</Banner>
        </Card>
      </Page>
    );
  }

  return (
    <Page>
      <TopBar shortCode={shortCode} />
      <div className="mb-4 mt-2 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-xl font-bold">{pocket.item.description}</h1>
          <p className="text-xs text-muted">
            {pocket.item.category} · you are the {pocket.your_role}
          </p>
        </div>
        <StateBadge state={pocket.state} />
      </div>

      <div className="grid gap-4">
        <MoneyCard p={pocket} />
        <TimersCard p={pocket} />
        <Actions p={pocket} shortCode={shortCode} token={token} refresh={refresh} />
      </div>
    </Page>
  );
}

function TopBar({ shortCode }: { shortCode: string }) {
  return (
    <div className="mb-2 flex items-center justify-between">
      <Link href="/" className="text-sm text-muted">
        ← EscrowPay
      </Link>
      <span className="font-mono text-xs text-muted">{shortCode}</span>
    </div>
  );
}

function MoneyCard({ p }: { p: PocketView }) {
  const m = p.money;
  return (
    <Card>
      <SectionTitle>Money</SectionTitle>
      {m.buyer_total_kobo !== undefined && p.your_role === "buyer" && (
        <Row label="You pay" value={<strong>{formatKobo(m.buyer_total_kobo)}</strong>} />
      )}
      {m.amount_kobo !== undefined && p.your_role === "vendor" && (
        <Row label="You receive" value={<strong>{formatKobo(m.amount_kobo)}</strong>} />
      )}
      {p.your_role === "broker" && (
        <>
          <Row label="Buyer pays" value={formatKobo(m.buyer_total_kobo)} />
          <Row label="Vendor allocation" value={formatKobo(m.amount_kobo)} />
          <Row label="Your commission" value={formatKobo(m.commission_kobo)} />
          <Row label="Protection fee" value={formatKobo(m.premium_kobo)} />
        </>
      )}
      {p.counterparty && (
        <Row
          label={p.counterparty.role}
          value={p.counterparty.display_name || "not yet joined"}
        />
      )}
      {p.counterparty?.delivery_address && (
        <Row label="Deliver to" value={p.counterparty.delivery_address} />
      )}
    </Card>
  );
}

function TimersCard({ p }: { p: PocketView }) {
  const rows: React.ReactNode[] = [];
  if (p.state === "CREATED" && p.timers.funding_expires_at) {
    rows.push(<Countdown key="f" deadline={p.timers.funding_expires_at} label="Funding window closes in" />);
  }
  if (p.state === "FUNDED" && p.timers.delivery_deadline) {
    rows.push(<Countdown key="d" deadline={p.timers.delivery_deadline} label="Deliver before" />);
  }
  if (p.state === "DELIVERED_PENDING" && p.timers.settle_after) {
    rows.push(<Countdown key="s" deadline={p.timers.settle_after} label="Settles in" />);
  }
  if (p.state === "FROZEN" && p.timers.grace_deadline) {
    rows.push(<Countdown key="g" deadline={p.timers.grace_deadline} label="Grace period ends in" />);
  }
  if (rows.length === 0) return null;
  return (
    <Card>
      <SectionTitle>Timing</SectionTitle>
      <div className="grid gap-1">{rows}</div>
    </Card>
  );
}

// useAction wraps a single mutating call with busy/error state and a refresh.
function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = useCallback(async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }, []);
  return { busy, error, run };
}

interface ActionProps {
  p: PocketView;
  shortCode: string;
  token: string;
  refresh: () => Promise<void>;
}

function Actions(props: ActionProps) {
  const { p } = props;
  const isBuyer = p.your_role === "buyer";

  // The broker is a read-only observer: they set the terms and watch the
  // lifecycle, but never enter codes or hold funds. Their commission is a
  // settlement leg the platform disburses.
  if (p.your_role === "broker") {
    return <BrokerPanel p={p} />;
  }

  if (p.state === "DRAFT") {
    return p.you.accepted ? <WaitingPanel /> : <AcceptPanel {...props} />;
  }
  if (p.state === "CREATED") {
    return isBuyer ? <PayPanel {...props} /> : <AwaitPaymentPanel {...props} />;
  }
  if (p.state === "FUNDED") {
    return isBuyer ? <ReleaseCodePanel {...props} /> : <EnterCodePanel {...props} />;
  }
  if (p.state === "DELIVERED_PENDING") {
    return isBuyer ? <DeliveredBuyerPanel {...props} /> : <DeliveredVendorPanel />;
  }
  if (p.state === "FROZEN") {
    return isBuyer ? (
      <div className="grid gap-4">
        <ReleaseCodePanel {...props} />
        <FrozenBuyerPanel {...props} />
      </div>
    ) : (
      <FrozenVendorPanel {...props} />
    );
  }
  if (p.state === "DISPUTED") {
    return <DisputePanel {...props} />;
  }
  return <TerminalPanel p={p} />;
}

function AcceptPanel({ p, shortCode, token, refresh }: ActionProps) {
  const isBuyer = p.your_role === "buyer";
  const [phone, setPhone] = useState("");
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const { busy, error, run } = useAction();

  return (
    <Card>
      <SectionTitle>Review &amp; accept</SectionTitle>
      <p className="mb-4 text-sm text-muted">
        You&rsquo;re joining as the {p.your_role}. Accepting locks in these terms for both sides.
      </p>
      <div className="grid gap-3">
        <Field label="Your phone">
          <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+2348020000002" />
        </Field>
        <Field label="Your name">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Bola Buyer" />
        </Field>
        {isBuyer && (
          <Field label="Delivery address">
            <Input value={address} onChange={(e) => setAddress(e.target.value)} placeholder="14 Marina Road, Lagos" />
          </Field>
        )}
        {error && <Banner tone="red">{error}</Banner>}
        <Button
          tone="primary"
          disabled={busy || !phone.trim()}
          onClick={() =>
            run(async () => {
              if (!p.you.claimed) await api.claim(shortCode, token, phone.trim(), name.trim());
              await api.accept(shortCode, token, isBuyer ? address.trim() : undefined);
              await refresh();
            })
          }
        >
          {busy ? <Spinner /> : "Accept terms"}
        </Button>
      </div>
    </Card>
  );
}

function BrokerPanel({ p }: { p: PocketView }) {
  const messages: Partial<Record<string, { tone: "emerald" | "amber" | "red" | "blue" | "zinc"; text: string }>> = {
    DRAFT: { tone: "blue", text: "Waiting for the vendor and buyer to accept your terms." },
    CREATED: { tone: "blue", text: "Both sides accepted. Waiting for the buyer to fund the pocket." },
    FUNDED: { tone: "emerald", text: "Funded. Waiting for the vendor to deliver and the buyer to confirm." },
    DELIVERED_PENDING: {
      tone: "emerald",
      text: "Handoff confirmed. Your commission is released when the inspection window closes.",
    },
    SETTLED: { tone: "emerald", text: "Settled. Your commission was paid alongside the vendor." },
    DISPUTED: { tone: "red", text: "This pocket is in dispute. You'll be notified of the outcome." },
    FROZEN: { tone: "amber", text: "Delivery wasn't confirmed in time. The pocket is frozen." },
    REFUNDED: { tone: "zinc", text: "Refunded to the buyer. No commission was due." },
    CANCELLED: { tone: "zinc", text: "This pocket was cancelled." },
    EXPIRED: { tone: "zinc", text: "This pocket expired before it was funded." },
  };
  const m = messages[p.state] ?? { tone: "zinc" as const, text: "Observing this pocket." };
  return (
    <Card>
      <SectionTitle>Broker view</SectionTitle>
      <Banner tone={m.tone}>{m.text}</Banner>
    </Card>
  );
}

function WaitingPanel() {
  return (
    <Card>
      <Banner tone="blue">You&rsquo;ve accepted. Waiting for the other side to accept.</Banner>
    </Card>
  );
}

function PayPanel(props: ActionProps) {
  const { p, refresh } = props;
  const { busy, error, run } = useAction();
  return (
    <Card>
      <SectionTitle>Payment</SectionTitle>
      <p className="mb-4 text-sm text-muted">
        Your money is held by the bank, not the vendor. It&rsquo;s released only after you confirm
        delivery with a Release Code.
      </p>
      {error && <Banner tone="red">{error}</Banner>}
      <Button
        tone="primary"
        disabled={busy}
        onClick={() => run(async () => {
          await api.simulateFunding(p.id);
          await refresh();
        })}
      >
        {busy ? <Spinner /> : `Pay ${formatKobo(p.money.buyer_total_kobo)} (sandbox)`}
      </Button>
      <p className="mt-2 text-center text-xs text-muted">Sandbox: no real charge is made.</p>
      <CancelButton {...props} inline />
    </Card>
  );
}

function AwaitPaymentPanel({ p, shortCode, token, refresh }: ActionProps) {
  return (
    <Card>
      <Banner tone="blue">Waiting for the buyer to fund the pocket.</Banner>
      <div className="mt-3">
        <CancelButton p={p} shortCode={shortCode} token={token} refresh={refresh} inline />
      </div>
    </Card>
  );
}

function ReleaseCodePanel({ p, token }: ActionProps) {
  const [code, setCode] = useState<string | null>(null);
  const { busy, error, run } = useAction();
  return (
    <Card>
      <SectionTitle>Your Release Code</SectionTitle>
      <Banner tone="amber">
        Only read this code out once the item is physically in your hands. Sharing it releases the
        money to the vendor.
      </Banner>
      <div className="mt-4">
        {code ? (
          <div className="rounded-xl border border-emerald-500/40 bg-emerald-500/10 py-6 text-center">
            <div className="font-mono text-4xl font-bold tracking-[0.3em] text-emerald-700 dark:text-emerald-300">
              {code}
            </div>
          </div>
        ) : (
          <Button
            tone="neutral"
            disabled={busy}
            onClick={() => run(async () => {
              const r = await api.releaseCode(p.id, token);
              setCode(r.release_code);
            })}
          >
            {busy ? <Spinner /> : "Reveal Release Code"}
          </Button>
        )}
        {error && <div className="mt-3"><Banner tone="red">{error}</Banner></div>}
      </div>
    </Card>
  );
}

function EnterCodePanel({ p, shortCode, token, refresh }: ActionProps) {
  const [code, setCode] = useState("");
  const [result, setResult] = useState<CodeEntryResult | null>(null);
  const { busy, error, run } = useAction();
  const frozen = p.state === "FROZEN";
  return (
    <Card>
      <SectionTitle>{frozen ? "Enter the buyer's code (late)" : "Confirm handoff"}</SectionTitle>
      <p className="mb-4 text-sm text-muted">
        Ask the buyer for their 4-digit Release Code once they have the item. Entering it releases
        your payment.
      </p>
      <div className="grid gap-3">
        <Input
          inputMode="numeric"
          maxLength={4}
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
          placeholder="0000"
          className="text-center font-mono text-2xl tracking-[0.4em]"
        />
        {result && !result.accepted && (
          <Banner tone="amber">
            {result.locked
              ? "Code locked after too many attempts. Contact support."
              : `Wrong code — ${result.attempts_remaining} attempt${result.attempts_remaining === 1 ? "" : "s"} left.`}
          </Banner>
        )}
        {error && <Banner tone="red">{error}</Banner>}
        <Button
          tone="primary"
          disabled={busy || code.length !== 4}
          onClick={() => run(async () => {
            const r = await api.enterCode(shortCode, token, code);
            setResult(r);
            setCode("");
            await refresh();
          })}
        >
          {busy ? <Spinner /> : "Submit code"}
        </Button>
      </div>
    </Card>
  );
}

function DeliveredBuyerPanel({ shortCode, token, refresh }: ActionProps) {
  const { busy, error, run } = useAction();
  return (
    <div className="grid gap-4">
      <Card>
        <Banner tone="emerald">
          Handoff confirmed. If everything&rsquo;s fine, the payment settles automatically when the
          inspection window ends.
        </Banner>
        {error && <div className="mt-3"><Banner tone="red">{error}</Banner></div>}
        <div className="mt-4">
          <Button tone="danger" disabled={busy} onClick={() => run(async () => {
            await api.reportIssue(shortCode, token);
            await refresh();
          })}>
            {busy ? <Spinner /> : "Report a problem"}
          </Button>
        </div>
      </Card>
      <EvidenceUpload shortCode={shortCode} token={token} type="unboxing_video" refresh={refresh} />
    </div>
  );
}

function DeliveredVendorPanel() {
  return (
    <Card>
      <Banner tone="emerald">
        Handoff confirmed. Your payment settles automatically once the buyer&rsquo;s inspection
        window closes.
      </Banner>
    </Card>
  );
}

function FrozenBuyerPanel({ shortCode, token, refresh }: ActionProps) {
  const { busy, error, run } = useAction();
  return (
    <Card>
      <SectionTitle>Delivery not confirmed</SectionTitle>
      <p className="mb-4 text-sm text-muted">
        The delivery window closed without a Release Code. If the item never arrived, tell us — your
        refund is armed once you attest, and issued when the grace period ends.
      </p>
      {error && <Banner tone="red">{error}</Banner>}
      <div className="grid gap-3">
        <Button tone="primary" disabled={busy} onClick={() => run(async () => {
          await api.attestNonReceipt(shortCode, token);
          await refresh();
        })}>
          {busy ? <Spinner /> : "I never received the item"}
        </Button>
        <Button tone="ghost" disabled={busy} onClick={() => run(async () => {
          await api.openDispute(shortCode, token);
          await refresh();
        })}>
          Open a dispute instead
        </Button>
      </div>
    </Card>
  );
}

function FrozenVendorPanel(props: ActionProps) {
  const { shortCode, token, refresh } = props;
  const { busy, error, run } = useAction();
  return (
    <div className="grid gap-4">
      <EnterCodePanel {...props} />
      <Card>
        <SectionTitle>Couldn&rsquo;t deliver?</SectionTitle>
        {error && <Banner tone="red">{error}</Banner>}
        <Button tone="danger" disabled={busy} onClick={() => run(async () => {
          await api.confirmDispatchFailure(shortCode, token);
          await refresh();
        })}>
          {busy ? <Spinner /> : "Confirm failure & refund buyer"}
        </Button>
      </Card>
    </div>
  );
}

function DisputePanel({ p, shortCode, token, refresh }: ActionProps) {
  const [dispute, setDispute] = useState<DisputeView | null>(null);
  const { busy, error, run } = useAction();
  const isVendor = p.your_role === "vendor";

  const load = useCallback(async () => {
    try {
      setDispute(await api.getDispute(shortCode, token));
    } catch {
      /* dispute record may lag the state poll; retry on next refresh */
    }
  }, [shortCode, token]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async fetch: dispute state lands after the awaited response
    load();
  }, [load, p.state]);

  return (
    <div className="grid gap-4">
      <Card>
        <SectionTitle>Dispute</SectionTitle>
        <Banner tone="red">
          This pocket is in dispute{dispute ? ` (${dispute.class.replace(/_/g, " ")})` : ""}. An
          arbitrator will review the evidence.
        </Banner>
        {dispute && dispute.evidence.length > 0 && (
          <div className="mt-3 grid gap-1">
            {dispute.evidence.map((ev) => (
              <Row
                key={ev.id}
                label={`${ev.party} · ${ev.type.replace(/_/g, " ")}`}
                value={
                  ev.within_window === undefined ? "—" : ev.within_window ? "in window" : "late"
                }
              />
            ))}
          </div>
        )}
        {isVendor && (
          <div className="mt-4">
            {error && <div className="mb-3"><Banner tone="red">{error}</Banner></div>}
            <Button tone="primary" disabled={busy} onClick={() => run(async () => {
              await api.concede(shortCode, token);
              await refresh();
            })}>
              {busy ? <Spinner /> : "Concede & refund the buyer"}
            </Button>
          </div>
        )}
      </Card>
      <EvidenceUpload
        shortCode={shortCode}
        token={token}
        type={isVendor ? "dispatch_proof" : "unboxing_video"}
        refresh={async () => {
          await refresh();
          await load();
        }}
      />
    </div>
  );
}

function EvidenceUpload({
  shortCode,
  token,
  type,
  refresh,
}: {
  shortCode: string;
  token: string;
  type: string;
  refresh: () => Promise<void>;
}) {
  const { busy, error, run } = useAction();
  const [done, setDone] = useState(false);
  return (
    <Card>
      <SectionTitle>Add evidence</SectionTitle>
      <p className="mb-3 text-sm text-muted">
        {type === "unboxing_video"
          ? "Record an unboxing video in the app. Captured within the protection window, it counts as proof."
          : "Attach proof of dispatch or packing."}
      </p>
      {error && <div className="mb-3"><Banner tone="red">{error}</Banner></div>}
      {done && <div className="mb-3"><Banner tone="emerald">Evidence uploaded.</Banner></div>}
      <label className="inline-flex h-12 w-full cursor-pointer items-center justify-center rounded-xl border border-border bg-transparent text-sm font-semibold hover:bg-black/5 dark:hover:bg-white/5">
        {busy ? <Spinner /> : "Capture / choose file"}
        <input
          type="file"
          accept="video/*,image/*"
          capture="environment"
          className="hidden"
          disabled={busy}
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            setDone(false);
            run(async () => {
              await api.uploadEvidence(shortCode, token, type, file);
              setDone(true);
              await refresh();
            });
          }}
        />
      </label>
    </Card>
  );
}

function TerminalPanel({ p }: { p: PocketView }) {
  const map: Record<string, { tone: "emerald" | "red" | "zinc"; text: string }> = {
    SETTLED: { tone: "emerald", text: "Settled. Funds were released to the vendor." },
    REFUNDED: { tone: "emerald", text: "Refunded. The buyer was paid back in full." },
    CANCELLED: { tone: "zinc", text: "This pocket was cancelled." },
    EXPIRED: { tone: "zinc", text: "This pocket expired before it was funded." },
  };
  const m = map[p.state] ?? { tone: "zinc" as const, text: "This pocket is closed." };
  return (
    <Card>
      <Banner tone={m.tone}>{m.text}</Banner>
    </Card>
  );
}

function CancelButton({ p, shortCode, token, refresh, inline }: ActionProps & { inline?: boolean }) {
  const { busy, error, run } = useAction();
  const canCancel =
    p.state === "DRAFT" ||
    p.state === "CREATED" ||
    (p.state === "FUNDED" && p.your_role === "vendor");
  if (!canCancel) return null;
  const realToken = token || "";
  return (
    <div className={inline ? "mt-3" : ""}>
      {error && <div className="mb-2"><Banner tone="red">{error}</Banner></div>}
      <button
        className="w-full text-center text-xs text-muted underline disabled:opacity-50"
        disabled={busy}
        onClick={() => run(async () => {
          await api.cancel(shortCode, realToken);
          await refresh();
        })}
      >
        {p.state === "FUNDED" ? "Cancel & refund buyer" : "Cancel this pocket"}
      </button>
    </div>
  );
}
