"use client";

import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useTranslations } from "next-intl";
import type { APIUpstreamAuthType, APIUpstreamCredential } from "@/lib/api/api-services";
export { credentialComplete, credentialFor } from "./upstream-credential";

function valueFor(credential: APIUpstreamCredential, key: keyof APIUpstreamCredential) { return credential[key] ?? ""; }

export function UpstreamCredentialFields({ idPrefix, authType, credential = {}, onChange, error }: { idPrefix: string; authType: APIUpstreamAuthType; credential?: APIUpstreamCredential; onChange: (credential: APIUpstreamCredential) => void; error?: string }) {
  const t = useTranslations("apiServices");
  const change = (key: keyof APIUpstreamCredential, value: string) => onChange({ ...credential, [key]: value });
  if (authType === "none") return null;
  if (authType === "bearer") return <Field data-invalid={Boolean(error) || undefined}><FieldLabel htmlFor={`${idPrefix}-bearer-token`}>{t("bearerToken")}</FieldLabel><Input id={`${idPrefix}-bearer-token`} type="password" autoComplete="new-password" aria-invalid={Boolean(error)} value={valueFor(credential, "bearer_token")} onChange={(event) => change("bearer_token", event.target.value)} />{error ? <FieldError>{error}</FieldError> : null}</Field>;
  if (authType === "header") return <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2"><Field data-invalid={Boolean(error) || undefined}><FieldLabel htmlFor={`${idPrefix}-header-name`}>{t("headerName")}</FieldLabel><Input id={`${idPrefix}-header-name`} aria-invalid={Boolean(error)} value={valueFor(credential, "header_name")} onChange={(event) => change("header_name", event.target.value)} />{error ? <FieldError>{error}</FieldError> : null}</Field><Field><FieldLabel htmlFor={`${idPrefix}-header-value`}>{t("headerValue")}</FieldLabel><Input id={`${idPrefix}-header-value`} type="password" autoComplete="new-password" value={valueFor(credential, "header_value")} onChange={(event) => change("header_value", event.target.value)} /></Field></FieldGroup>;
  if (authType === "query") return <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2"><Field data-invalid={Boolean(error) || undefined}><FieldLabel htmlFor={`${idPrefix}-query-name`}>{t("queryName")}</FieldLabel><Input id={`${idPrefix}-query-name`} aria-invalid={Boolean(error)} value={valueFor(credential, "query_name")} onChange={(event) => change("query_name", event.target.value)} />{error ? <FieldError>{error}</FieldError> : null}</Field><Field><FieldLabel htmlFor={`${idPrefix}-query-value`}>{t("queryValue")}</FieldLabel><Input id={`${idPrefix}-query-value`} type="password" autoComplete="new-password" value={valueFor(credential, "query_value")} onChange={(event) => change("query_value", event.target.value)} /></Field></FieldGroup>;
  return <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2"><Field data-invalid={Boolean(error) || undefined}><FieldLabel htmlFor={`${idPrefix}-basic-username`}>{t("basicUsername")}</FieldLabel><Input id={`${idPrefix}-basic-username`} aria-invalid={Boolean(error)} value={valueFor(credential, "basic_username")} onChange={(event) => change("basic_username", event.target.value)} />{error ? <FieldError>{error}</FieldError> : null}</Field><Field><FieldLabel htmlFor={`${idPrefix}-basic-password`}>{t("basicPassword")}</FieldLabel><Input id={`${idPrefix}-basic-password`} type="password" autoComplete="new-password" value={valueFor(credential, "basic_password")} onChange={(event) => change("basic_password", event.target.value)} /></Field></FieldGroup>;
}
