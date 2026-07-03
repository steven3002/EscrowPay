"use client";

import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type User } from "./api";

// useMe resolves the signed-in account once per mount. `user` is null while
// loading and when signed out; `known` distinguishes "still checking" from
// "checked, signed out" so screens can gate without flashing.
export function useMe() {
  const [user, setUser] = useState<User | null>(null);
  const [known, setKnown] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const res = await api.me();
      setUser(res.user);
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
        setUser(null);
      }
    } finally {
      setKnown(true);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- async fetch: state lands after the awaited response
    refresh();
  }, [refresh]);

  return { user, known, refresh };
}
