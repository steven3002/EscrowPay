"use client";

import { usePathname } from "next/navigation";
import { useEffect, useRef, useState, type CSSProperties } from "react";

// TopProgress is the thin accent bar that streaks across the top of the page
// during navigation (YouTube/NProgress style). It starts when an internal link
// is clicked and completes when the pathname actually changes, with a safety
// timeout so a cancelled navigation can't leave it stuck.
export function TopProgress() {
  const pathname = usePathname();
  const [phase, setPhase] = useState<"idle" | "loading" | "done">("idle");
  const prev = useRef(pathname);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const anchor = (e.target as HTMLElement | null)?.closest?.("a");
      if (!anchor) return;
      const href = anchor.getAttribute("href");
      if (!href || anchor.getAttribute("target") === "_blank") return;
      if (href.startsWith("#") || href.startsWith("mailto:") || href.startsWith("tel:")) return;
      let dest: URL;
      try {
        dest = new URL(href, window.location.href);
      } catch {
        return;
      }
      if (dest.origin !== window.location.origin) return;
      if (dest.pathname === window.location.pathname && dest.search === window.location.search) return;
      setPhase("loading");
    }
    document.addEventListener("click", onClick, true);
    return () => document.removeEventListener("click", onClick, true);
  }, []);

  // Complete the bar once the route commits.
  useEffect(() => {
    if (prev.current === pathname) return;
    prev.current = pathname;
    setPhase("done");
    const t = setTimeout(() => setPhase("idle"), 350);
    return () => clearTimeout(t);
  }, [pathname]);

  // Fallback so a cancelled navigation resets the bar.
  useEffect(() => {
    if (phase !== "loading") return;
    const t = setTimeout(() => setPhase("idle"), 12000);
    return () => clearTimeout(t);
  }, [phase]);

  const style: CSSProperties =
    phase === "loading"
      ? { width: "90%", opacity: 1, transitionDuration: "10s" }
      : phase === "done"
        ? { width: "100%", opacity: 0, transitionDuration: "0.3s" }
        : { width: "0%", opacity: 0, transitionDuration: "0s" };

  return (
    <div aria-hidden className="pointer-events-none fixed inset-x-0 top-0 z-[60] h-[3px]">
      <div
        className="h-full rounded-r-full bg-accent shadow-[0_0_10px_rgba(5,150,105,0.7)] transition-[width,opacity] ease-out"
        style={style}
      />
    </div>
  );
}
