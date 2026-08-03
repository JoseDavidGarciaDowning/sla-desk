import { ClerkProvider } from "@clerk/nextjs";
import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";

import { Providers } from "./providers";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "SLA Desk",
  description:
    "Support desk with SLA tracking: customers raise tickets, agents triage, assign and answer them before the clock runs out.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  // ClerkProvider wraps everything, including the query provider: a query can
  // need a token, and the hook that reads one only works inside it.
  //
  // signInUrl and signUpUrl here cover client-side navigation only — the links
  // Clerk's own components render, and redirects it performs in the browser.
  //
  // They do NOT govern the redirect from auth.protect(). That runs on the
  // server, where these props are React context it never sees; setting them
  // only here left the redirect pointing at Clerk's hosted pages while the
  // sign-in route in this app sat unreachable. The server side is configured on
  // clerkMiddleware in proxy.ts, and both are needed.
  //
  // In code rather than in NEXT_PUBLIC_ variables because these are not
  // deployment configuration: they are this app's own routes, identical in
  // every environment, and one that forgot them would fall back to the hosted
  // pages without any error to notice.
  return (
    <ClerkProvider signInUrl="/sign-in" signUpUrl="/sign-up">
      <html
        lang="en"
        className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
      >
        <body className="min-h-full flex flex-col">
          <Providers>{children}</Providers>
        </body>
      </html>
    </ClerkProvider>
  );
}
