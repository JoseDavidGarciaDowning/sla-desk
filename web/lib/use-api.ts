"use client";

import { useAuth } from "@clerk/nextjs";
import { useCallback } from "react";

import { apiFetch } from "@/lib/api";

/**
 * Returns a fetch that carries the caller's Clerk session token.
 *
 * The token is attached here and nowhere else. Doing it per component would
 * mean every new call site is one more place to forget it, and forgetting it
 * looks like a 401 rather than like a missing header — a bug that reads as an
 * auth problem when it is a plumbing problem.
 *
 * `getToken()` returns a short-lived token and refreshes it as needed, so it is
 * called per request rather than held. A signed-out caller gets null, the
 * request goes without the header, and the API answers 401 — which is correct,
 * and which the query cache turns into a redirect.
 */
export function useApiFetch() {
  const { getToken } = useAuth();

  return useCallback(
    async <T,>(path: string, init?: RequestInit): Promise<T> => {
      const token = await getToken();

      return apiFetch<T>(path, {
        ...init,
        headers: {
          ...init?.headers,
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
      });
    },
    [getToken],
  );
}
