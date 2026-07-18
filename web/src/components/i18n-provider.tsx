"use client";

import { NextIntlClientProvider } from "next-intl";
import { useCallback, useSyncExternalStore } from "react";
import { defaultLocale, locales, type Locale } from "@/i18n/config";
import zhMessages from "@/i18n/zh.json";
import enMessages from "@/i18n/en.json";

const allMessages: Record<Locale, Record<string, unknown>> = {
  zh: zhMessages as Record<string, unknown>,
  en: enMessages as Record<string, unknown>,
};

function getLocaleFromCookie(): Locale {
  if (typeof document === "undefined") return defaultLocale;
  const match = document.cookie.match(/(?:^|;\s*)locale=([^;]*)/);
  const val = match?.[1];
  if (val && locales.includes(val as Locale)) return val as Locale;
  return defaultLocale;
}

export function I18nProvider({
  children,
  initialLocale,
  initialMessages,
}: {
  children: React.ReactNode;
  initialLocale: string;
  initialMessages: Record<string, unknown>;
}) {
  const serverLocale = locales.includes(initialLocale as Locale)
    ? (initialLocale as Locale)
    : defaultLocale;
  const subscribe = useCallback((notify: () => void) => {
    window.addEventListener("locale-change", notify);
    return () => window.removeEventListener("locale-change", notify);
  }, []);
  const getServerLocale = useCallback(() => serverLocale, [serverLocale]);
  const locale = useSyncExternalStore(subscribe, getLocaleFromCookie, getServerLocale);
  const messages = locale === serverLocale ? initialMessages : allMessages[locale];

  return (
    <NextIntlClientProvider locale={locale} messages={messages}>
      {children}
    </NextIntlClientProvider>
  );
}
