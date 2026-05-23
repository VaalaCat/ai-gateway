import type { Metadata } from "next";
import localFont from "next/font/local";
import { getLocale, getMessages } from "next-intl/server";
import { I18nProvider } from "@/components/i18n-provider";
import { Providers } from "@/components/providers";
import "./globals.css";

const datatype = localFont({
  src: "../../public/fonts/datatype-vf.woff2",
  variable: "--font-datatype",
  display: "swap",
});

export const metadata: Metadata = {
  title: "AI Gateway",
  description: "Distributed AI Gateway Management",
};

export default async function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const locale = await getLocale();
  const messages = await getMessages();

  return (
    <html lang={locale} suppressHydrationWarning>
      <body className={`antialiased ${datatype.variable}`}>
        <I18nProvider
          initialLocale={locale}
          initialMessages={messages as Record<string, unknown>}
        >
          <Providers>{children}</Providers>
        </I18nProvider>
      </body>
    </html>
  );
}
