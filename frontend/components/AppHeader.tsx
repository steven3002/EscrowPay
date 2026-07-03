"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, type User } from "@/lib/api";

// AppHeader is the brand row plus the account chrome: a sign-in link when
// anonymous, and the account's name linking to the dashboard (with sign-out)
// when a session exists.
export function AppHeader({ user, known, next }: { user: User | null; known: boolean; next?: string }) {
  const router = useRouter();

  async function signOut() {
    try {
      await api.logout();
    } finally {
      router.refresh();
      window.location.reload();
    }
  }

  return (
    <div className="mb-4 flex items-center justify-between gap-3">
      <Link href="/" className="flex items-center gap-2">
        {/* eslint-disable-next-line @next/next/no-img-element -- static inline SVG mark, no optimization needed */}
        <img src="/icon.svg" alt="" width={28} height={28} className="h-7 w-7" />
        <span className="text-base font-bold tracking-tight">EscrowPay</span>
      </Link>
      {!known ? null : user ? (
        <div className="flex items-center gap-3">
          <Link href="/dashboard" className="max-w-[9rem] truncate text-sm font-semibold">
            {user.display_name || "My pockets"}
          </Link>
          <button onClick={signOut} className="text-xs text-muted underline">
            Sign out
          </button>
        </div>
      ) : (
        <Link
          href={`/login${next ? `?next=${encodeURIComponent(next)}` : ""}`}
          className="text-sm font-semibold text-emerald-700 underline dark:text-emerald-300"
        >
          Sign in
        </Link>
      )}
    </div>
  );
}
