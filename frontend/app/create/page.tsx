"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";
import {
  Banner,
  Button,
  Card,
  Field,
  Input,
  LinkButton,
  Page,
  Row,
  Select,
  SectionTitle,
  Spinner,
} from "@/components/ui";
import { api, ApiError, type CreateResponse, type Role, type Structure } from "@/lib/api";
import { formatKobo } from "@/lib/format";
import { remember } from "@/lib/recent";
import { useMe } from "@/lib/useMe";

const CATEGORIES = ["electronics", "phones", "fashion", "beauty", "home", "general"];

function CreateScreen() {
  const { user, known } = useMe();
  // Home's suggestion chips deep-link the deal shape: ?as=vendor|buyer preselects
  // the creator's role; ?type=brokered opens the three-party flow.
  const params = useSearchParams();
  const [structure, setStructure] = useState<Structure>(
    params.get("type") === "brokered" ? "brokered" : "p2p",
  );
  const [creatorRole, setCreatorRole] = useState<Role>(
    params.get("as") === "buyer" ? "buyer" : "vendor",
  );
  const [item, setItem] = useState("");
  const [category, setCategory] = useState("electronics");
  const [amountNaira, setAmountNaira] = useState("");
  const [commissionNaira, setCommissionNaira] = useState("");
  const [premiumNaira, setPremiumNaira] = useState("");
  const [mode, setMode] = useState<"cooldown" | "instant">("cooldown");
  const [inspectionHours, setInspectionHours] = useState("24");
  const [deliveryHours, setDeliveryHours] = useState("48");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CreateResponse | null>(null);

  const brokered = structure === "brokered";
  // When a home chip deep-links the shape, commit to it: hide the type/role
  // selectors and show a compact summary with a "Change" escape hatch.
  const lockedFromChip =
    params.get("type") === "brokered" || params.get("as") === "buyer" || params.get("as") === "vendor";
  const lockedLabel =
    params.get("type") === "brokered"
      ? "Brokered — you connect a buyer and a seller"
      : params.get("as") === "buyer"
        ? "Direct — you're the buyer"
        : "Direct — you're the vendor";
  const effectiveCreatorRole: Role = brokered ? "broker" : creatorRole;
  const amountKobo = Math.round(Number(amountNaira || 0) * 100);
  const commissionKobo = brokered ? Math.round(Number(commissionNaira || 0) * 100) : 0;
  const premiumKobo = Math.round(Number(premiumNaira || 0) * 100);
  const suggestedPremium = useMemo(
    () => (amountKobo > 0 ? Math.max(5000, Math.round((amountKobo + commissionKobo) * 0.02)) : 0),
    [amountKobo, commissionKobo],
  );

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!item.trim()) return setError("Describe the item.");
    if (amountKobo <= 0) return setError("Enter an amount.");

    setBusy(true);
    try {
      const res = await api.createPocket({
        structure,
        creator_role: effectiveCreatorRole,
        mode,
        inspection_window_minutes: mode === "cooldown" ? Number(inspectionHours || 0) * 60 : 0,
        delivery_window_minutes: Number(deliveryHours || 0) * 60,
        amount_kobo: amountKobo,
        commission_kobo: commissionKobo,
        premium_kobo: premiumKobo,
        item_description: item.trim(),
        category,
      });
      const myToken = res.tokens[effectiveCreatorRole];
      if (myToken) {
        remember({
          shortCode: res.short_code,
          pocketId: res.pocket_id,
          role: effectiveCreatorRole,
          token: myToken,
          item: item.trim(),
        });
      }
      setResult(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  if (result) {
    return <ResultPanel result={result} brokered={brokered} creatorRole={effectiveCreatorRole} />;
  }

  if (known && !user) {
    return (
      <Page>
        <Card>
          <SectionTitle>New pocket</SectionTitle>
          <p className="mb-4 text-sm text-muted">
            Sign in first — the pocket is bound to your account, so only you can manage it.
          </p>
          <LinkButton href="/login?next=/create" tone="primary">
            Sign in to continue
          </LinkButton>
        </Card>
      </Page>
    );
  }

  return (
    <Page>
      <header className="mb-6">
        <h1 className="text-3xl font-semibold tracking-tight">New pocket</h1>
        <p className="mt-1.5 text-sm text-muted">
          Set the terms both sides agree to. The bank holds the money until the buyer confirms the
          handoff.
        </p>
      </header>

      <Card>
      <form onSubmit={submit} className="grid gap-4">
        {lockedFromChip ? (
          <div className="flex items-center justify-between gap-3 rounded-xl border border-border bg-surface-muted px-4 py-3">
            <div className="min-w-0">
              <p className="text-xs font-medium text-muted">Deal type</p>
              <p className="truncate text-sm font-semibold">{lockedLabel}</p>
            </div>
            <Link href="/create" className="shrink-0 text-xs font-semibold text-accent hover:underline">
              Change
            </Link>
          </div>
        ) : (
          <>
            <Field label="Transaction type">
              <Select value={structure} onChange={(e) => setStructure(e.target.value as Structure)}>
                <option value="p2p">Direct — you and one other person</option>
                <option value="brokered">Brokered — I connect a buyer and a seller</option>
              </Select>
            </Field>

            {!brokered && (
              <Field label="I am the">
                <Select value={creatorRole} onChange={(e) => setCreatorRole(e.target.value as Role)}>
                  <option value="vendor">Vendor (selling)</option>
                  <option value="buyer">Buyer (paying)</option>
                </Select>
              </Field>
            )}
          </>
        )}

        <Field label="Item">
          <Input value={item} onChange={(e) => setItem(e.target.value)} placeholder="e.g. Nikon Z6 camera" />
        </Field>

        <Field label="Category">
          <Select value={category} onChange={(e) => setCategory(e.target.value)}>
            {CATEGORIES.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </Select>
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label={brokered ? "Vendor gets (₦)" : "Amount (₦)"}>
            <Input inputMode="decimal" value={amountNaira} onChange={(e) => setAmountNaira(e.target.value)} placeholder="10000" />
          </Field>
          {brokered ? (
            <Field label="Your commission (₦)">
              <Input inputMode="decimal" value={commissionNaira} onChange={(e) => setCommissionNaira(e.target.value)} placeholder="1500" />
            </Field>
          ) : (
            <Field label="Protection fee (₦)">
              <Input
                inputMode="decimal"
                value={premiumNaira}
                onChange={(e) => setPremiumNaira(e.target.value)}
                placeholder={suggestedPremium ? String(suggestedPremium / 100) : "200"}
              />
            </Field>
          )}
        </div>

        {brokered && (
          <Field label="Protection fee (₦)">
            <Input
              inputMode="decimal"
              value={premiumNaira}
              onChange={(e) => setPremiumNaira(e.target.value)}
              placeholder={suggestedPremium ? String(suggestedPremium / 100) : "200"}
            />
          </Field>
        )}

        <Field label="Protection mode">
          <Select value={mode} onChange={(e) => setMode(e.target.value as "cooldown" | "instant")}>
            <option value="cooldown">Cooldown — buyer gets an inspection window</option>
            <option value="instant">Instant — settle immediately on handoff</option>
          </Select>
        </Field>

        <div className="grid grid-cols-2 gap-3">
          {mode === "cooldown" && (
            <Field label="Inspection window (hours)">
              <Input inputMode="numeric" value={inspectionHours} onChange={(e) => setInspectionHours(e.target.value)} />
            </Field>
          )}
          <Field label="Delivery window (hours)">
            <Input inputMode="numeric" value={deliveryHours} onChange={(e) => setDeliveryHours(e.target.value)} />
          </Field>
        </div>

        {amountKobo > 0 && (
          <Card className="!bg-surface-muted !shadow-none">
            <Row label={brokered ? "Vendor allocation" : "Item amount"} value={formatKobo(amountKobo)} />
            {brokered && <Row label="Your commission" value={formatKobo(commissionKobo)} />}
            <Row label="Protection fee" value={formatKobo(premiumKobo)} />
            <div className="my-1 border-t border-border" />
            <Row label="Buyer pays" value={<strong>{formatKobo(amountKobo + commissionKobo + premiumKobo)}</strong>} />
          </Card>
        )}

        {error && <Banner tone="red">{error}</Banner>}

        <Button type="submit" tone="primary" disabled={busy}>
          {busy ? <Spinner /> : "Create pocket"}
        </Button>
      </form>
      </Card>
    </Page>
  );
}

export default function CreatePage() {
  return (
    <Suspense
      fallback={
        <Page>
          <div className="flex flex-1 items-center justify-center pt-24 text-muted">
            <Spinner />
          </div>
        </Page>
      }
    >
      <CreateScreen />
    </Suspense>
  );
}

function ResultPanel({
  result,
  brokered,
  creatorRole,
}: {
  result: CreateResponse;
  brokered: boolean;
  creatorRole: Role;
}) {
  const myToken = result.tokens[creatorRole];
  return (
    <Page>
      <BackLink />
      <h1 className="mb-1 text-3xl font-semibold tracking-tight">Pocket created</h1>
      <p className="mb-6 text-sm text-muted">
        {brokered
          ? "Send each link to the right person. The buyer's link stays inert until the vendor accepts."
          : "Share the counterparty link. Funds stay with the bank until the buyer confirms delivery."}
      </p>

      {brokered ? (
        <div className="mb-4 grid gap-3">
          <Card>
            <SectionTitle>Send to the vendor</SectionTitle>
            <ShareLink path={`/p/${result.short_code}?t=${result.tokens.vendor}`} />
          </Card>
          <Card>
            <SectionTitle>Send to the buyer</SectionTitle>
            <ShareLink path={`/p/${result.short_code}?t=${result.tokens.buyer}`} />
          </Card>
        </div>
      ) : (
        <Card className="mb-4">
          <SectionTitle>Send this to the {result.counterparty_role}</SectionTitle>
          <ShareLink path={`/p/${result.short_code}?t=${result.tokens[result.counterparty_role ?? "buyer"]}`} />
        </Card>
      )}

      <div className="grid gap-3">
        {myToken && (
          <LinkButton href={`/p/${result.short_code}?t=${myToken}`} tone="primary">
            Open my {creatorRole} view
          </LinkButton>
        )}
        <LinkButton href="/create" tone="ghost">
          Create another
        </LinkButton>
      </div>
    </Page>
  );
}

function BackLink() {
  return (
    <Link href="/" className="mb-4 inline-block text-sm text-muted">
      ← Home
    </Link>
  );
}

function ShareLink({ path }: { path: string }) {
  const [copied, setCopied] = useState(false);
  const full = typeof window !== "undefined" ? window.location.origin + path : path;
  async function copy() {
    try {
      await navigator.clipboard.writeText(full);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable; the link is still selectable below */
    }
  }
  return (
    <div className="grid gap-2">
      <div className="break-all rounded-xl border border-border bg-surface-muted px-3 py-2 font-mono text-xs">
        {full}
      </div>
      <Button tone="neutral" onClick={copy} type="button">
        {copied ? "Copied ✓" : "Copy link"}
      </Button>
    </div>
  );
}
