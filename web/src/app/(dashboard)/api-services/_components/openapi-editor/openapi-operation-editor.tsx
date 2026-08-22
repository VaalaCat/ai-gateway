"use client";

import { Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import type { JSONValue } from "./openapi-editor-state";
import { OpenAPIJSONField, useOpenAPIEditorFieldState } from "./openapi-json-field";
import { OpenAPIParameterEditor } from "./openapi-parameter-editor";

export const OPENAPI_HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

function stringValue(value: JSONValue | undefined) { return typeof value === "string" ? value : ""; }

export function OpenAPIOperationEditor({ path, method, existingMethods, operation, snapshotKey, fieldPrefix, disabled, onMethodChange, onChange, onRemove }: {
  path: string; method: string; existingMethods: string[]; operation: Record<string, JSONValue>; snapshotKey: string; fieldPrefix: string; disabled: boolean;
  onMethodChange: (method: string) => void; onChange: (operation: Record<string, JSONValue>) => void; onRemove: () => void;
}) {
  const t = useTranslations("apiServices");
  const editorState = useOpenAPIEditorFieldState();
  const parameters = Array.isArray(operation.parameters) ? operation.parameters as Array<Record<string, JSONValue>> : [];
  const setField = (name: string, value: JSONValue) => onChange({ ...operation, [name]: value });
  return <section className="flex min-w-0 flex-col gap-4 border-t pt-4" aria-label={`${method} ${path}`}>
    <div className="flex flex-wrap items-center gap-2">
      <Select value={method} disabled={disabled} onValueChange={(nextMethod) => { onMethodChange(nextMethod); editorState?.movePrefix(fieldPrefix, fieldPrefix.replace(/:operation:[^:]+$/, `:operation:${nextMethod}`)); }}><SelectTrigger aria-label={t("openAPIMethodLabel")}><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{OPENAPI_HTTP_METHODS.filter((item) => item === method || !existingMethods.includes(item)).map((item) => <SelectItem key={item} value={item}>{item}</SelectItem>)}</SelectGroup></SelectContent></Select>
      <Button type="button" variant="ghost" disabled={disabled} onClick={() => { editorState?.deletePrefix(fieldPrefix); onRemove(); }}><Trash2 data-icon="inline-start" />{t("openAPIRemoveOperation")}</Button>
    </div>
    <FieldGroup>
      <Field><FieldLabel htmlFor={`${fieldPrefix}-summary`}>{t("openAPIOperationSummary")}</FieldLabel><Input id={`${fieldPrefix}-summary`} value={stringValue(operation.summary)} disabled={disabled} onChange={(event) => setField("summary", event.target.value)} /></Field>
      <Field><FieldLabel htmlFor={`${fieldPrefix}-description`}>{t("openAPIOperationDescription")}</FieldLabel><Input id={`${fieldPrefix}-description`} value={stringValue(operation.description)} disabled={disabled} onChange={(event) => setField("description", event.target.value)} /></Field>
    </FieldGroup>
    <OpenAPIParameterEditor scope="operation" fieldPrefix={fieldPrefix} snapshotKey={snapshotKey} parameters={parameters} disabled={disabled} onAdd={() => onChange({ ...operation, parameters: [...parameters, { name: "", in: "query", required: false, schema: {} }] })} onChange={(index, parameter) => onChange({ ...operation, parameters: parameters.map((current, currentIndex) => currentIndex === index ? parameter : current) })} onRemove={(index) => onChange({ ...operation, parameters: parameters.filter((_, currentIndex) => currentIndex !== index) })} />
    <OpenAPIJSONField fieldKey={`${fieldPrefix}:requestBody`} snapshotKey={snapshotKey} id={`${fieldPrefix}-request-body`} label={t("openAPIRequestBody")} value={operation.requestBody} objectOnly disabled={disabled} onChange={(value) => setField("requestBody", value)} />
    <OpenAPIJSONField fieldKey={`${fieldPrefix}:responses`} snapshotKey={snapshotKey} id={`${fieldPrefix}-responses`} label={t("openAPIResponses")} value={operation.responses} objectOnly disabled={disabled} onChange={(value) => setField("responses", value)} />
  </section>;
}
