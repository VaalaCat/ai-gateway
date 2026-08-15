"use client";

import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { invalidOverrideHeader, type ExampleHeaderRow } from "./route-form/route-form-state";

let nextHeaderID = 0;

export function UpstreamHeaderFields({ idPrefix, rows, onChange, t }: { idPrefix: string; rows: ExampleHeaderRow[]; onChange: (rows: ExampleHeaderRow[]) => void; t: (key: string) => string }) {
  const patchRow = (id: string, patch: Partial<ExampleHeaderRow>) => onChange(rows.map((row) => row.id === id ? { ...row, ...patch } : row));
  return <FieldSet><FieldLegend>{t("headerOverride")}</FieldLegend><FieldGroup>
    {rows.map((row, index) => { const invalid = invalidOverrideHeader(row, rows); return <FieldGroup key={row.id} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <Field data-invalid={invalid || undefined}><FieldLabel htmlFor={`${idPrefix}-header-name-${index}`}>{t("headerName")}</FieldLabel><Input id={`${idPrefix}-header-name-${index}`} aria-invalid={invalid} value={row.name} onChange={(event) => patchRow(row.id, { name: event.target.value })} />{invalid ? <FieldError>{t("invalidHeaderOverride")}</FieldError> : null}</Field>
      <Field><FieldLabel htmlFor={`${idPrefix}-header-value-${index}`}>{t("headerValue")}</FieldLabel><div className="flex gap-2"><Input id={`${idPrefix}-header-value-${index}`} value={row.value} onChange={(event) => patchRow(row.id, { value: event.target.value })} /><Button type="button" variant="outline" aria-label={t("removeHeader")} onClick={() => onChange(rows.filter((candidate) => candidate.id !== row.id))}>{t("remove")}</Button></div></Field>
    </FieldGroup>; })}
    <Button type="button" variant="outline" onClick={() => onChange([...rows, { id: `${idPrefix}-header-${++nextHeaderID}`, name: "", value: "" }])}>{t("addHeader")}</Button>
  </FieldGroup></FieldSet>;
}
