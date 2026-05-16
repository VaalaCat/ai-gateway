import { getRequestConfig } from "next-intl/server";
import { defaultLocale, type Locale, locales } from "./config";

export default getRequestConfig(async () => {
  const localeFromEnv = process.env.NEXT_PUBLIC_LOCALE;
  const locale: Locale =
    localeFromEnv && locales.includes(localeFromEnv as Locale)
      ? (localeFromEnv as Locale)
      : defaultLocale;

  return {
    locale,
    messages: (await import(`./${locale}.json`)).default,
  };
});
