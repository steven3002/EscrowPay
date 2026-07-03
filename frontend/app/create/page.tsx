"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
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

const CATEGORIES = ["electronics", "phones", "fashion", "beauty", "home", "general"];

export default function CreatePage() {
  const [structure, setStructure] = useState<Structure>("p2p");
  const [creatorRole, setCreatorRole] = useState<Role>("vendor");
  const [item, setItem] = useState("");
  const [category, setCategory] = useState("electronics");
  const [amountNaira, setAmountNaira] = useState("");
  const [commissionNaira, setCommissionNaira] = useState("");
  const [premiumNaira, setPremiumNaira] = useState("");
  const [mode, setMode] = useState<"cooldown" | "instant">("cooldown");
  const [inspectionHours, setInspectionHours] = useState("24");
  const [deliveryHours, setDeliveryHours] = useState("48");
  const [phone, setPhone] = useState("");
  const [displayName, setDisplayName] = useState("");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CreateResponse | null>(null);

  const brokered = structure === "brokered";
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
    if (!phone.trim()) return setError("Your phone number is required.");

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
        creator: { phone: phone.trim(), display_name: displayName.trim() },
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

  return (
    <Page>
      <BackLink />
      <h1 className="mb-1 text-2xl font-bold">New pocket</h1>
      <p className="mb-6 text-sm text-muted">Set the terms every side will see.</p>

      <form onSubmit={submit} className="grid gap-4">
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

        <div className="grid grid-cols-2 gap-3">
          <Field label="Your phone">
            <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+2348010000001" />
          </Field>
          <Field label="Your name">
            <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="Ada Stores" />
          </Field>
        </div>

        {amountKobo > 0 && (
          <Card className="!bg-background">
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
    </Page>
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
      <h1 className="mb-1 text-2xl font-bold">Pocket created</h1>
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
      <div className="break-all rounded-xl border border-border bg-background px-3 py-2 font-mono text-xs">
        {full}
      </div>
      <Button tone="neutral" onClick={copy} type="button">
        {copied ? "Copied ✓" : "Copy link"}
      </Button>
    </div>
  );
}
