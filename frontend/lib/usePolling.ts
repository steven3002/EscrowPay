import { useEffect } from "react";

// usePolling runs an async fetch immediately, then on an interval and whenever
// the tab regains focus, so every screen reconstructs server truth without a
// client-side source of truth. The fetch updates state only after its awaited
// response resolves, so it is not a synchronous setState-in-effect despite the
// lint heuristic below.
export function usePolling(fetcher: () => void, intervalMs: number) {
  useEffect(() => {
    fetcher();
    const id = setInterval(fetcher, intervalMs);
    const onFocus = () => fetcher();
    window.addEventListener("focus", onFocus);
    return () => {
      clearInterval(id);
      window.removeEventListener("focus", onFocus);
    };
  }, [fetcher, intervalMs]);
}
