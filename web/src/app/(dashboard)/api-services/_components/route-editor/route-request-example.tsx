"use client";

import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { APIRequestExample } from "@/lib/api/api-services";
import { exampleHeadersObject, invalidExampleHeader, requestExampleByteLength, safeExampleQuery, safeExampleSubpath, unsafeExampleHeader, type ExampleHeaderRow, type RouteFormValues } from "../route-form/route-form-state";
import { isStandardRouteHTTPMethod, STANDARD_ROUTE_HTTP_METHODS } from "./standard-http-methods";

export function RouteRequestExample({ values, onChange, t }: {
  values: RouteFormValues;
  onChange: (patch: Partial<RouteFormValues>) => void;
  t: (key: string, values?: Record<string, string | number | Date>) => string;
}) {
  const updateExample = (patch: Partial<APIRequestExample>) => onChange({ exampleRequest: { ...values.exampleRequest, ...patch } });
  const updateHeaderRows = (exampleHeaderRows: ExampleHeaderRow[]) => onChange({
    exampleHeaderRows,
    exampleRequest: { ...values.exampleRequest, headers: exampleHeadersObject(exampleHeaderRows) },
  });
  const methods: readonly string[] = values.allowedMethods.length ? values.allowedMethods : STANDARD_ROUTE_HTTP_METHODS;
  const methodInvalid = values.exampleEnabled && (!isStandardRouteHTTPMethod(values.exampleRequest.method) || !methods.includes(values.exampleRequest.method));
  const pathInvalid = values.exampleEnabled && !safeExampleSubpath(values.exampleRequest.subpath);
  const queryInvalid = values.exampleEnabled && !safeExampleQuery(values.exampleRequest.query);
  const bodyBytes = requestExampleByteLength({ ...values.exampleRequest, headers: exampleHeadersObject(values.exampleHeaderRows) });
  const bodyInvalid = bodyBytes > 64 * 1024;
  const toggle = (enabled: boolean) => onChange({
    exampleEnabled: enabled,
    exampleRequest: enabled && !values.exampleRequest.method
      ? { ...values.exampleRequest, method: methods[0] ?? "GET" }
      : values.exampleRequest,
  });

  return <FieldSet>
    <FieldLegend>{t("exampleRequest")}</FieldLegend>
    <FieldGroup className="gap-4">
      <Field orientation="horizontal">
        <Checkbox id="api-route-example-enabled" checked={values.exampleEnabled} onCheckedChange={(checked) => toggle(checked === true)} />
        <div className="flex flex-col gap-1">
          <FieldLabel htmlFor="api-route-example-enabled">{t("configureRequestExample")}</FieldLabel>
          <FieldDescription>{t("requestExampleDescription")}</FieldDescription>
        </div>
      </Field>
      {values.exampleEnabled ? <FieldGroup className="gap-4">
        <div className="grid min-w-0 gap-4 sm:grid-cols-2">
          <Field data-invalid={methodInvalid || undefined}>
            <FieldLabel htmlFor="api-route-example-method">{t("method")}</FieldLabel>
            <Select value={values.exampleRequest.method} onValueChange={(method) => updateExample({ method })}>
              <SelectTrigger id="api-route-example-method" aria-label={t("exampleMethod")} aria-invalid={methodInvalid} className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>{methods.map((method) => <SelectItem key={method} value={method}>{method}</SelectItem>)}</SelectGroup></SelectContent>
            </Select>
            {methodInvalid ? <FieldError>{t("invalidExampleMethod")}</FieldError> : null}
          </Field>
          {values.forwardSubpath ? <Field data-invalid={pathInvalid || undefined}>
            <FieldLabel htmlFor="api-route-example-subpath">{t("subpath")}</FieldLabel>
            <Input id="api-route-example-subpath" aria-label={t("exampleSubpath")} aria-invalid={pathInvalid} value={values.exampleRequest.subpath} onChange={(event) => updateExample({ subpath: event.target.value })} />
            {pathInvalid ? <FieldError>{t("invalidExamplePath")}</FieldError> : null}
          </Field> : null}
        </div>
        <Field data-invalid={queryInvalid || undefined}>
          <FieldLabel htmlFor="api-route-example-query">{t("query")}</FieldLabel>
          <Input id="api-route-example-query" aria-label={t("exampleQuery")} aria-invalid={queryInvalid} value={values.exampleRequest.query} onChange={(event) => updateExample({ query: event.target.value })} />
          {queryInvalid ? <FieldError>{t("invalidExampleQuery")}</FieldError> : null}
        </Field>
        <FieldSet>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <FieldLegend variant="label">{t("headers")}</FieldLegend>
            <Button type="button" variant="outline" size="sm" onClick={() => {
              let suffix = 1;
              let name = "X-Header";
              const names = new Set(values.exampleHeaderRows.map((row) => row.name.toLowerCase()));
              while (names.has(name.toLowerCase())) name = `X-Header-${++suffix}`;
              let id = `example-header-${values.exampleHeaderRows.length}`;
              while (values.exampleHeaderRows.some((row) => row.id === id)) id = `example-header-${values.exampleHeaderRows.length}-${suffix++}`;
              updateHeaderRows([...values.exampleHeaderRows, { id, name, value: "" }]);
            }}><Plus data-icon="inline-start" />{t("addHeader")}</Button>
          </div>
          {values.exampleHeaderRows.length ? <FieldGroup className="gap-3">{values.exampleHeaderRows.map((row, index) => {
            const invalid = invalidExampleHeader(row, values.exampleHeaderRows);
            const update = (patch: Partial<ExampleHeaderRow>) => updateHeaderRows(values.exampleHeaderRows.map((candidate) => candidate.id === row.id ? { ...candidate, ...patch } : candidate));
            return <Field key={row.id} data-invalid={invalid || undefined}>
              <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
                <Input aria-label={`${t("headerName")} ${index + 1}`} aria-invalid={invalid} value={row.name} onChange={(event) => update({ name: event.target.value })} />
                <Input aria-label={`${t("headerValue")} ${index + 1}`} aria-invalid={invalid} value={row.value} onChange={(event) => update({ value: event.target.value })} />
                <Button type="button" variant="ghost" size="icon-sm" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={`${t("removeHeader")} ${index + 1}`} onClick={() => updateHeaderRows(values.exampleHeaderRows.filter((candidate) => candidate.id !== row.id))}><Trash2 /></Button>
              </div>
              {invalid ? <FieldError>{t(unsafeExampleHeader(row.name.trim()) ? "unsafeExampleHeader" : "invalidExampleHeader")}</FieldError> : null}
            </Field>;
          })}</FieldGroup> : null}
        </FieldSet>
        {values.protocols.includes("http") ? <Field data-invalid={bodyInvalid || undefined}>
          <FieldLabel htmlFor="api-route-example-body">{t("body")}</FieldLabel>
          <Textarea id="api-route-example-body" aria-label={t("exampleBody")} aria-invalid={bodyInvalid} rows={5} value={values.exampleRequest.body} onChange={(event) => updateExample({ body: event.target.value })} />
          <FieldDescription>{t("bodySize", { bytes: bodyBytes })}</FieldDescription>
          {bodyInvalid ? <FieldError>{t("bodyTooLarge")}</FieldError> : null}
        </Field> : null}
        <Button type="button" variant="outline" size="sm" className="self-start" onClick={() => onChange({ exampleEnabled: false, exampleRequest: { method: "", subpath: "", query: "", headers: {}, body: "" }, exampleHeaderRows: [] })}>{t("clearRequestExample")}</Button>
      </FieldGroup> : null}
    </FieldGroup>
  </FieldSet>;
}
