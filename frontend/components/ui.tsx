import Link from "next/link";
import type { ButtonHTMLAttributes, ReactNode } from "react";

// Shared presentational primitives. They carry the app's mobile-first look —
// rounded surfaces, generous touch targets — so screens compose from a
// consistent kit rather than ad-hoc markup.

export function Page({ children }: { children: ReactNode }) {
  return (
    <div className="mx-auto flex min-h-screen w-full max-w-md flex-col px-4 pb-16 pt-6">
      {children}
    </div>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`rounded-2xl border border-border bg-surface p-5 shadow-sm ${className}`}
    >
      {children}
    </div>
  );
}

export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">{children}</h2>
  );
}

type Tone = "primary" | "neutral" | "danger" | "ghost";

const TONE: Record<Tone, string> = {
  primary: "bg-emerald-600 text-white hover:bg-emerald-700 disabled:bg-emerald-600/50",
  neutral:
    "bg-zinc-900 text-white hover:bg-zinc-800 disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-white",
  danger: "bg-red-600 text-white hover:bg-red-700 disabled:bg-red-600/50",
  ghost:
    "border border-border bg-transparent text-foreground hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50",
};

export function Button({
  tone = "neutral",
  className = "",
  children,
  ...props
}: { tone?: Tone } & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      className={`inline-flex h-12 w-full items-center justify-center gap-2 rounded-xl px-4 text-sm font-semibold transition-colors disabled:cursor-not-allowed ${TONE[tone]} ${className}`}
      {...props}
    >
      {children}
    </button>
  );
}

export function LinkButton({
  href,
  tone = "neutral",
  children,
}: {
  href: string;
  tone?: Tone;
  children: ReactNode;
}) {
  return (
    <Link
      href={href}
      className={`inline-flex h-12 w-full items-center justify-center gap-2 rounded-xl px-4 text-sm font-semibold transition-colors ${TONE[tone]}`}
    >
      {children}
    </Link>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium text-muted">{label}</span>
      {children}
    </label>
  );
}

const inputBase =
  "h-12 w-full rounded-xl border border-border bg-background px-3 text-base text-foreground outline-none focus:border-emerald-500 focus:ring-2 focus:ring-emerald-500/30";

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={inputBase} {...props} />;
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={inputBase} {...props} />;
}

const BADGE_TONE: Record<string, string> = {
  emerald: "bg-emerald-100 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300",
  amber: "bg-amber-100 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300",
  red: "bg-red-100 text-red-800 dark:bg-red-500/15 dark:text-red-300",
  blue: "bg-blue-100 text-blue-800 dark:bg-blue-500/15 dark:text-blue-300",
  zinc: "bg-zinc-200 text-zinc-700 dark:bg-zinc-700/40 dark:text-zinc-300",
};

export function Badge({ tone = "zinc", children }: { tone?: string; children: ReactNode }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-semibold ${BADGE_TONE[tone] ?? BADGE_TONE.zinc}`}
    >
      {children}
    </span>
  );
}

export function Banner({
  tone = "zinc",
  children,
}: {
  tone?: "emerald" | "amber" | "red" | "blue" | "zinc";
  children: ReactNode;
}) {
  const map: Record<string, string> = {
    emerald: "border-emerald-500/30 bg-emerald-500/10 text-emerald-900 dark:text-emerald-200",
    amber: "border-amber-500/30 bg-amber-500/10 text-amber-900 dark:text-amber-200",
    red: "border-red-500/30 bg-red-500/10 text-red-900 dark:text-red-200",
    blue: "border-blue-500/30 bg-blue-500/10 text-blue-900 dark:text-blue-200",
    zinc: "border-border bg-black/5 text-foreground dark:bg-white/5",
  };
  return (
    <div className={`rounded-xl border px-4 py-3 text-sm ${map[tone]}`}>{children}</div>
  );
}

export function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 py-1.5">
      <span className="text-sm text-muted">{label}</span>
      <span className="text-sm font-medium text-foreground">{value}</span>
    </div>
  );
}

export function Spinner() {
  return (
    <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
  );
}
