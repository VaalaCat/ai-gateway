"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { useEffect, useState } from "react";
import { Toaster } from "@/components/ui/sonner";
import { SiteBrandingRuntime } from "@/components/site-branding";
import { restorePublicConfig } from "@/lib/api/system";

export function Providers({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { staleTime: 30 * 1000, retry: 1 },
        },
      })
  );

  // Restore only after hydration: the exported HTML and first client render
  // must both show placeholders, never a browser-specific cached brand.
  useEffect(() => { restorePublicConfig(queryClient); }, [queryClient]);

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
        <SiteBrandingRuntime />
        {children}
        <Toaster richColors position="top-right" />
      </ThemeProvider>
    </QueryClientProvider>
  );
}
