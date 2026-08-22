"use client";

import { Plus, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import type { JSONValue } from "./openapi-editor-state";
import { OpenAPIJSONField, useOpenAPIEditorFieldState } from "./openapi-json-field";

const PARAMETER_LOCATIONS = ["query", "header", "path", "cookie"];

function stringValue(value: JSONValue | undefined) { return typeof value === "string" ? value : ""; }

export function OpenAPIParameterEditor({ scope, fieldPrefix, snapshotKey, parameters, disabled, onAdd, onChange, onRemove }: {
  scope: "path" | "operation"; fieldPrefix: string; snapshotKey: string;
  parameters: Array<Record<string, JSONValue>>; disabled: boolean;
  onAdd: () => void; onChange: (index: number, parameter: Record<string, JSONValue>) => void; onRemove: (index: number) => void;
}) {
  const t = useTranslations("apiServices");
  const editorState = useOpenAPIEditorFieldState();
  const scopeKey = `${fieldPrefix}:parameters`;
  const identities = editorState?.parameterIDs(scopeKey, parameters.length) ?? parameters.map((_, index) => String(index));
  const update = (index: number, name: string, value: JSONValue) => onChange(index, { ...parameters[index], [name]: value });
  return <section className="flex min-w-0 flex-col gap-3" aria-label={t(scope === "path" ? "openAPIPathParameters" : "openAPIOperationParameters")}>
    <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t("openAPIParameterName")}</TableHead><TableHead>{t("openAPIParameterLocation")}</TableHead><TableHead>{t("openAPIParameterRequired")}</TableHead><TableHead>{t("openAPIParameterSchema")}</TableHead><TableHead><span className="sr-only">{t("openAPIParameterActions")}</span></TableHead></TableRow></TableHeader><TableBody>
      {parameters.length === 0 ? <TableRow><TableCell colSpan={5} className="text-muted-foreground">{t("openAPIParametersEmpty")}</TableCell></TableRow> : parameters.map((parameter, index) => {
        const displayIndex = index + 1;
        const identity = identities[index]!;
        const fieldKey = `${scopeKey}:${identity}:schema`;
        return <TableRow key={identity}>
          <TableCell><Input aria-label={t("openAPIParameterNameAt", { index: displayIndex })} value={stringValue(parameter.name)} disabled={disabled} onChange={(event) => update(index, "name", event.target.value)} /></TableCell>
          <TableCell><Select value={stringValue(parameter.in) || "query"} disabled={disabled} onValueChange={(value) => update(index, "in", value)}><SelectTrigger aria-label={t("openAPIParameterLocationAt", { index: displayIndex })}><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{PARAMETER_LOCATIONS.map((location) => <SelectItem key={location} value={location}>{location}</SelectItem>)}</SelectGroup></SelectContent></Select></TableCell>
          <TableCell><Checkbox aria-label={t("openAPIParameterRequiredAt", { index: displayIndex })} checked={parameter.required === true} disabled={disabled} onCheckedChange={(checked) => update(index, "required", checked === true)} /></TableCell>
          <TableCell className="min-w-72"><OpenAPIJSONField fieldKey={fieldKey} snapshotKey={snapshotKey} id={fieldKey} label={t("openAPIParameterSchemaAt", { index: displayIndex })} value={parameter.schema} objectOnly disabled={disabled} onChange={(value) => update(index, "schema", value)} /></TableCell>
          <TableCell><Button type="button" variant="ghost" size="icon" aria-label={t("openAPIRemoveParameter", { index: displayIndex })} disabled={disabled} onClick={() => { editorState?.deleteField(fieldKey); editorState?.removeParameter(scopeKey, index); onRemove(index); }}><Trash2 data-icon="inline-start" /></Button></TableCell>
        </TableRow>;
      })}
    </TableBody></Table></div>
    <Button type="button" variant="outline" className="self-start" disabled={disabled} onClick={() => { editorState?.addParameter(scopeKey); onAdd(); }}><Plus data-icon="inline-start" />{t(scope === "path" ? "openAPIAddPathParameter" : "openAPIAddOperationParameter")}</Button>
  </section>;
}
