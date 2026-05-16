"use client";

import { NextIntlClientProvider } from "next-intl";
import { useEffect, useState } from "react";
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
  const [locale, setLocale] = useState<Locale>(initialLocale as Locale);
  const [messages, setMessages] =
    useState<Record<string, unknown>>(initialMessages);

  useEffect(() => {
    const cookieLocale = getLocaleFromCookie();
    if (cookieLocale !== locale) {
      setLocale(cookieLocale);
      setMessages(allMessages[cookieLocale]);
    }
  }, []);

  useEffect(() => {
    const handler = (e: Event) => {
      const locale = (e as CustomEvent<Locale>).detail;
      setLocale(locale);
      setMessages(allMessages[locale]);
    };
    window.addEventListener("locale-change", handler);
    return () => window.removeEventListener("locale-change", handler);
  }, []);

  return (
    <NextIntlClientProvider locale={locale} messages={messages}>
      {children}
    </NextIntlClientProvider>
  );
}
