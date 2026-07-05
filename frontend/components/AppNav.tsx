"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { useMe } from "@/lib/useMe";
import {
  ChevronRightIcon,
  HomeIcon,
  PlusIcon,
  PocketsIcon,
  ScaleIcon,
  UserIcon,
} from "@/components/icons";
import type { ComponentType, SVGProps } from "react";

// AppNav is the app's persistent chrome. On desktop it's a left sidebar that
// collapses to an icon rail (the width is a CSS var toggled via a data attribute
// on <html>, persisted in localStorage); on mobile it's a fixed bottom tab bar.
// Active state comes from the pathname, account state from the session.

type NavItem = {
  href: string;
  label: string;
  Icon: ComponentType<SVGProps<SVGSVGElement>>;
};

const SIDEBAR_NAV: NavItem[] = [
  { href: "/", label: "Home", Icon: HomeIcon },
  { href: "/dashboard", label: "Your pockets", Icon: PocketsIcon },
  { href: "/admin", label: "Arbitration desk", Icon: ScaleIcon },
];

const TABS: (NavItem & { primary?: boolean })[] = [
  { href: "/", label: "Home", Icon: HomeIcon },
  { href: "/dashboard", label: "Pockets", Icon: PocketsIcon },
  { href: "/create", label: "Create", Icon: PlusIcon, primary: true },
  { href: "/admin", label: "Desk", Icon: ScaleIcon },
];

function useIsActive() {
  const pathname = usePathname();
  return (href: string) =>
    href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`);
}

// Purely imperative: flips the <html> attribute + localStorage so styling is
// CSS-driven and there's no React state to desync on hydration.
function toggleSidebar() {
  const el = document.documentElement;
  const collapsed = el.getAttribute("data-sidebar") === "collapsed";
  if (collapsed) el.removeAttribute("data-sidebar");
  else el.setAttribute("data-sidebar", "collapsed");
  try {
    localStorage.setItem("ep-sidebar", collapsed ? "expanded" : "collapsed");
  } catch {
    /* storage unavailable; the toggle still works for this session */
  }
}

export function AppNav() {
  const { user, known, refresh } = useMe();
  const pathname = usePathname();
  const router = useRouter();
  const isActive = useIsActive();

  async function signOut() {
    try {
      await api.logout();
    } finally {
      await refresh();
      router.refresh();
      window.location.reload();
    }
  }

  const next = pathname && pathname !== "/" ? `?next=${encodeURIComponent(pathname)}` : "";

  return (
    <>
      {/* Desktop: collapsible left sidebar. */}
      <aside className="ep-sidebar fixed inset-y-0 left-0 z-40 hidden md:flex md:flex-col md:border-r md:border-border md:bg-surface">
        <button
          type="button"
          onClick={toggleSidebar}
          aria-label="Toggle sidebar"
          className="absolute -right-3 top-6 z-10 flex h-6 w-6 items-center justify-center rounded-full border border-border bg-surface text-muted shadow-[var(--shadow-card)] transition-colors hover:text-foreground"
        >
          <ChevronRightIcon className="ep-toggle-chevron h-4 w-4" />
        </button>

        <div className="flex h-full flex-col gap-1 p-3">
          <Link href="/" title="EscrowPay" className="ep-navitem mb-4 flex items-center gap-2 rounded-xl px-2 py-1.5">
            {/* eslint-disable-next-line @next/next/no-img-element -- static inline SVG mark */}
            <img src="/icon.svg" alt="" width={30} height={30} className="h-[30px] w-[30px] shrink-0" />
            <span className="ep-hide-collapsed text-lg font-bold tracking-tight">EscrowPay</span>
            <span className="ep-hide-collapsed rounded-full bg-accent-soft px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-accent-strong">
              Sandbox
            </span>
          </Link>

          <Link
            href="/create"
            title="New pocket"
            aria-current={isActive("/create") ? "page" : undefined}
            className="ep-navitem mb-3 flex h-11 items-center justify-center gap-2 rounded-xl bg-accent px-4 text-sm font-semibold text-white shadow-[var(--shadow-card)] transition-colors hover:bg-accent-strong"
          >
            <PlusIcon className="h-[18px] w-[18px] shrink-0" />
            <span className="ep-hide-collapsed">New pocket</span>
          </Link>

          <nav className="grid gap-1">
            {SIDEBAR_NAV.map(({ href, label, Icon }) => {
              const active = isActive(href);
              return (
                <Link
                  key={href}
                  href={href}
                  title={label}
                  aria-current={active ? "page" : undefined}
                  className={`ep-navitem flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition-colors ${
                    active
                      ? "bg-accent-soft text-accent-strong"
                      : "text-muted hover:bg-surface-muted hover:text-foreground"
                  }`}
                >
                  <Icon className="h-5 w-5 shrink-0" />
                  <span className="ep-hide-collapsed">{label}</span>
                </Link>
              );
            })}
          </nav>

          <div className="mt-auto">
            <div className="ep-hide-collapsed mb-3 rounded-xl bg-surface-muted p-3 text-xs leading-relaxed text-muted">
              The bank holds every naira. EscrowPay only decides when it moves.
            </div>
            {!known ? (
              <div className="h-11" />
            ) : user ? (
              <div className="flex items-center gap-2">
                <Link
                  href="/dashboard"
                  title={user.display_name || "My pockets"}
                  className="ep-navitem flex min-w-0 flex-1 items-center gap-2.5 rounded-xl p-1.5 transition-colors hover:bg-surface-muted"
                >
                  <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent text-xs font-bold text-white">
                    {(user.display_name || "?").trim().charAt(0).toUpperCase()}
                  </span>
                  <span className="ep-hide-collapsed min-w-0">
                    <span className="block truncate text-sm font-semibold">{user.display_name || "My pockets"}</span>
                    <span className="block text-xs text-muted">View pockets</span>
                  </span>
                </Link>
                <button
                  onClick={signOut}
                  title="Sign out"
                  className="ep-hide-collapsed rounded-lg px-2 py-1 text-xs font-medium text-muted transition-colors hover:bg-surface-muted hover:text-foreground"
                >
                  Sign out
                </button>
              </div>
            ) : (
              <Link
                href={`/login${next}`}
                title="Sign in"
                className="ep-navitem flex h-11 w-full items-center justify-center gap-2 rounded-xl border border-border bg-surface text-sm font-semibold text-foreground transition-colors hover:bg-surface-muted"
              >
                <UserIcon className="h-[18px] w-[18px] shrink-0" />
                <span className="ep-hide-collapsed">Sign in</span>
              </Link>
            )}
          </div>
        </div>
      </aside>

      {/* Mobile: fixed bottom tab bar. */}
      <nav className="pb-safe fixed inset-x-0 bottom-0 z-40 border-t border-border bg-surface/95 backdrop-blur md:hidden">
        <div className="mx-auto flex max-w-md items-stretch justify-around px-1">
          {TABS.map(({ href, label, Icon, primary }) => {
            const active = isActive(href);
            return (
              <Link
                key={href}
                href={href}
                aria-current={active ? "page" : undefined}
                className="flex flex-1 flex-col items-center gap-1 py-2 text-[11px] font-medium"
              >
                <span className="flex h-9 items-center justify-center">
                  {primary ? (
                    <span className="flex h-9 w-9 items-center justify-center rounded-full bg-accent text-white shadow-sm">
                      <Icon className="h-5 w-5" />
                    </span>
                  ) : (
                    <Icon className={`h-6 w-6 ${active ? "text-accent" : "text-muted"}`} />
                  )}
                </span>
                <span className={primary ? "text-foreground" : active ? "text-accent" : "text-muted"}>
                  {label}
                </span>
              </Link>
            );
          })}
          <AccountTab known={known} hasUser={Boolean(user)} name={user?.display_name} next={next} />
        </div>
      </nav>
    </>
  );
}

// AccountTab collapses the account chrome into the fifth mobile tab: a sign-in
// link when anonymous, or the account initial linking to the dashboard.
function AccountTab({
  known,
  hasUser,
  name,
  next,
}: {
  known: boolean;
  hasUser: boolean;
  name?: string;
  next: string;
}) {
  if (known && hasUser) {
    return (
      <Link
        href="/dashboard"
        className="flex flex-1 flex-col items-center gap-1 py-2 text-[11px] font-medium text-muted"
      >
        <span className="flex h-9 items-center justify-center">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-accent text-[11px] font-bold text-white">
            {(name || "?").trim().charAt(0).toUpperCase()}
          </span>
        </span>
        <span className="max-w-[4.5rem] truncate">{name || "Account"}</span>
      </Link>
    );
  }
  return (
    <Link
      href={`/login${next}`}
      className="flex flex-1 flex-col items-center gap-1 py-2 text-[11px] font-medium text-muted"
    >
      <span className="flex h-9 items-center justify-center">
        <UserIcon className="h-6 w-6" />
      </span>
      <span>Sign in</span>
    </Link>
  );
}
