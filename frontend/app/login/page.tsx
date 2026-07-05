"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useState } from "react";
import { api, ApiError, type AuthProviders } from "@/lib/api";
import { usePolling } from "@/lib/usePolling";
import { Banner, Button, Card, Field, Input, Page, SectionTitle, Spinner } from "@/components/ui";

// safeNext admits only same-site paths as post-login targets, mirroring the
// server-side rule.
function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//")) return "/dashboard";
  return next;
}

const OAUTH_ERRORS: Record<string, string> = {
  denied: "Google sign-in was cancelled.",
  flow: "The sign-in attempt expired. Please try again.",
  state: "The sign-in attempt could not be verified. Please try again.",
  verify: "Google sign-in could not be verified. Please try again.",
  account: "Your account could not be created. Please try again.",
  session: "Signing you in failed. Please try again.",
};

function LoginScreen() {
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));
  const oauthError = params.get("error");

  const [providers, setProviders] = useState<AuthProviders | null>(null);
  const [phone, setPhone] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const probe = useCallback(async () => {
    try {
      setProviders(await api.authProviders());
    } catch {
      /* transient; retried on the next poll */
    }
  }, []);
  usePolling(probe, 30000);

  async function demoSignIn(e: React.FormEvent) {
    e.preventDefault();
    if (!phone.trim()) return setError("Enter a phone number.");
    setBusy(true);
    setError(null);
    try {
      await api.demoLogin(phone.trim(), name.trim());
      router.push(next);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Sign-in failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page>
      <Link href="/" className="mb-4 inline-block text-sm text-muted">
        ← EscrowPay
      </Link>
      <h1 className="mb-1 text-2xl font-bold">Sign in</h1>
      <p className="mb-6 text-sm text-muted">
        Your account is what protects your pockets: only you can act on the deals you join.
      </p>

      {oauthError && (
        <div className="mb-4">
          <Banner tone="red">{OAUTH_ERRORS[oauthError] ?? "Sign-in failed. Please try again."}</Banner>
        </div>
      )}

      {providers === null ? (
        <div className="flex justify-center pt-10 text-muted">
          <Spinner />
        </div>
      ) : (
        <div className="grid gap-4">
          {providers.google && (
            <a
              href={`/api/auth/google/start?next=${encodeURIComponent(next)}`}
              className="inline-flex h-12 w-full items-center justify-center gap-2 rounded-xl border border-border bg-surface px-4 text-sm font-semibold hover:bg-surface-muted"
            >
              Continue with Google
            </a>
          )}

          {providers.demo && (
            <Card>
              <SectionTitle>Sandbox sign-in</SectionTitle>
              <p className="mb-3 text-sm text-muted">
                Demo accounts are keyed by phone number — no password, sandbox only.
              </p>
              <form onSubmit={demoSignIn} className="grid gap-3">
                <Field label="Phone">
                  <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+2348010000001" />
                </Field>
                <Field label="Name">
                  <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Ada Stores" />
                </Field>
                {error && <Banner tone="red">{error}</Banner>}
                <Button type="submit" tone="primary" disabled={busy}>
                  {busy ? <Spinner /> : "Sign in"}
                </Button>
              </form>
            </Card>
          )}

          {!providers.google && !providers.demo && (
            <Banner tone="amber">No sign-in method is configured on this deployment.</Banner>
          )}
        </div>
      )}
    </Page>
  );
}

export default function LoginPage() {
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
      <LoginScreen />
    </Suspense>
  );
}
