import Link from "next/link";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { ChevronRightIcon } from "@/components/icons";

// Shared presentational primitives. They carry the app's look — a clean light
// canvas, rounded white surfaces, generous touch targets — so screens compose
// from a consistent kit rather than ad-hoc markup.

export function Page({ children }: { children: ReactNode }) {
  // Centered column that fills the space under the nav. Extra bottom padding on
  // mobile clears the fixed bottom tab bar; desktop widens the readable width.
  return (
    <div className="mx-auto flex w-full max-w-md flex-1 flex-col px-4 pb-28 pt-6 md:max-w-2xl md:px-6 md:pb-16 md:pt-10">
      {children}
    </div>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`rounded-2xl border border-border bg-surface p-5 shadow-[var(--shadow-card)] ${className}`}
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
  primary: "bg-accent text-white hover:bg-accent-strong disabled:bg-accent/50",
  neutral: "bg-zinc-900 text-white hover:bg-zinc-800 disabled:opacity-50",
  danger: "bg-red-600 text-white hover:bg-red-700 disabled:bg-red-600/50",
  ghost: "border border-border bg-surface text-foreground hover:bg-surface-muted disabled:opacity-50",
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

// Chip is a pill-shaped secondary action, echoing the reference's row of
// quick-action chips under the hero.
export function Chip({ href, icon, children }: { href: string; icon?: ReactNode; children: ReactNode }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-2 rounded-full border border-border bg-surface px-4 py-2.5 text-sm font-semibold text-foreground shadow-[var(--shadow-card)] transition-colors hover:bg-surface-muted"
    >
      {icon && <span className="text-accent">{icon}</span>}
      {children}
    </Link>
  );
}

// ListRow is the reference's list-item pattern: a circular icon, a two-line
// label, optional trailing content, and a chevron. Used for pocket lists.
export function ListRow({
  href,
  icon,
  title,
  subtitle,
  trailing,
}: {
  href: string;
  icon?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  trailing?: ReactNode;
}) {
  return (
    <Link
      href={href}
      className="flex items-center gap-3 rounded-2xl border border-border bg-surface p-3.5 shadow-[var(--shadow-card)] transition-colors hover:bg-surface-muted"
    >
      {icon && (
        <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-accent-soft text-accent">
          {icon}
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-semibold">{title}</span>
        {subtitle && <span className="mt-0.5 block truncate text-xs text-muted">{subtitle}</span>}
      </span>
      {trailing}
      <ChevronRightIcon className="h-5 w-5 shrink-0 text-muted" />
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
  "h-12 w-full rounded-xl border border-border bg-surface px-3 text-base text-foreground outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/25";

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={inputBase} {...props} />;
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={inputBase} {...props} />;
}

const BADGE_TONE: Record<string, string> = {
  emerald: "bg-accent-soft text-accent-strong",
  amber: "bg-amber-100 text-amber-800",
  red: "bg-red-100 text-red-800",
  blue: "bg-blue-100 text-blue-800",
  zinc: "bg-zinc-100 text-zinc-700",
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
    emerald: "border-emerald-200 bg-emerald-50 text-emerald-900",
    amber: "border-amber-200 bg-amber-50 text-amber-900",
    red: "border-red-200 bg-red-50 text-red-900",
    blue: "border-blue-200 bg-blue-50 text-blue-900",
    zinc: "border-border bg-surface-muted text-foreground",
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

// Skeleton is a shimmering placeholder block (styling in globals.css) used while
// content loads, so screens reserve their layout instead of flashing a spinner.
export function Skeleton({ className = "" }: { className?: string }) {
  return <span className={`ep-skeleton block rounded-lg ${className}`} />;
}

// EmptyState is the standard "nothing here yet" panel: an icon, a title, a line
// of guidance, and a primary action — so empty screens tell the user what to do.
export function EmptyState({
  icon,
  title,
  children,
  action,
}: {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center rounded-2xl border border-dashed border-border bg-surface px-6 py-12 text-center">
      {icon && (
        <span className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-surface-muted text-muted">
          {icon}
        </span>
      )}
      <h3 className="text-base font-semibold">{title}</h3>
      {children && <p className="mt-1.5 max-w-sm text-sm text-muted">{children}</p>}
      {action && <div className="mt-5 w-full max-w-xs">{action}</div>}
    </div>
  );
}
