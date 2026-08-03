"use client";

import {
  environmentManager,
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";

import { ApiError } from "@/lib/api";

/**
 * How long a query's data is considered fresh.
 *
 * Zero would refetch on the client immediately after the server rendered the
 * same data, which is wasted work on every navigation. Thirty seconds is short
 * enough that an SLA timer is never meaningfully stale and long enough that
 * moving between the list and a ticket does not refetch.
 */
const STALE_TIME_MS = 30_000;

function makeQueryClient() {
  return new QueryClient({
    queryCache: new QueryCache({
      // Handled once, here, rather than at every call site. A 401 means the
      // session ended — the token expired, or it was revoked from another
      // device — and the only useful response is to send the user back through
      // Clerk. Reloading does that: the (customer) layout runs auth.protect()
      // on the server and issues the redirect.
      onError: (error) => {
        if (error instanceof ApiError && error.status === 401) {
          window.location.reload();
        }
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: STALE_TIME_MS,

        // Retrying a 401 or a 404 accomplishes nothing but delay. Only
        // transient failures are worth a second attempt.
        retry: (failureCount, error) => {
          if (error instanceof ApiError && error.status < 500) {
            return false;
          }
          return failureCount < 2;
        },
      },
      mutations: {
        // A failed mutation is never retried automatically. Creating a ticket
        // twice because the response was slow is worse than showing an error.
        retry: false,
      },
    },
  });
}

let browserQueryClient: QueryClient | undefined;

/**
 * On the server a fresh client per request, so one visitor's data can never be
 * served to another. In the browser a single client, created once — React can
 * throw away the first render if something suspends, and a client rebuilt on
 * each attempt would lose its cache every time.
 */
function getQueryClient() {
  if (environmentManager.isServer()) {
    return makeQueryClient();
  }
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={getQueryClient()}>
      {children}
    </QueryClientProvider>
  );
}
