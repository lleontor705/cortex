import type { Metadata } from "next";
import "./globals.css";
import { AuthProvider } from "@/lib/auth-context";
import { ToastProvider } from "@/lib/toast";
import AppShell from "@/components/AppShell";

export const metadata: Metadata = {
  title: "Cortex — Agent Memory Control Room",
  description: "High-performance memory and knowledge graph system for AI coding agents",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="es">
      <head>
        <meta charSet="utf-8" />
        {/* Runtime endpoint injection: the Go embedded-web handler answers
            /config.js (unauthenticated) with window.__CORTEX_WEB_CONFIG__, so
            the UI resolves its API origin at runtime instead of falling back to
            the localhost dev default. Must run before the app bundles read
            server-endpoint.ts. On hosts without the handler (next dev, static
            previews) it 404s harmlessly and the unmanaged fallback applies. */}
        <script src="/config.js" />
      </head>
      <body>
        <AuthProvider>
          <ToastProvider>
            <AppShell>{children}</AppShell>
          </ToastProvider>
        </AuthProvider>
      </body>
    </html>
  );
}
