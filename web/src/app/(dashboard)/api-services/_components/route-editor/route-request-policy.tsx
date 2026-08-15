"use client";

import { useState } from "react";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { APIProtocol } from "@/lib/api/api-services";
import type { RouteFormValues } from "../route-form/route-form-state";
import { STANDARD_ROUTE_HTTP_METHODS } from "./standard-http-methods";

const protocols: APIProtocol[] = ["http", "websocket"];

export function RouteRequestPolicy({ values, onChange, validationErrors = [], t }: { values: RouteFormValues; onChange: (patch: Partial<RouteFormValues>) => void; validationErrors?: string[]; t: (key: string, values?: Record<string, string | number | Date>) => string }) {
  const subprotocolKey = values.websocketSubprotocols.join("\u0000");
  const [subprotocolState, setSubprotocolState] = useState({ key: subprotocolKey, text: values.websocketSubprotocols.join(", ") });
  const subprotocolText = subprotocolState.key === subprotocolKey ? subprotocolState.text : values.websocketSubprotocols.join(", ");
  const toggleProtocol = (protocol: APIProtocol, checked: boolean) => onChange({ protocols: checked ? [...new Set([...values.protocols, protocol])] : values.protocols.filter((item) => item !== protocol) });
  const toggleMethod = (method: string, checked: boolean) => onChange({ allowedMethods: checked ? [...new Set([...values.allowedMethods, method])] : values.allowedMethods.filter((item) => item !== method) });
  return <FieldSet><FieldLegend>{t("requestPolicy")}</FieldLegend><FieldGroup>
    <FieldSet data-invalid={validationErrors.includes("protocolRequired") || undefined}><FieldLegend variant="label">{t("protocols")}</FieldLegend><FieldGroup className="grid grid-cols-1 gap-3 sm:grid-cols-2">{protocols.map((protocol) => <Field key={protocol} orientation="horizontal"><Checkbox id={`api-route-protocol-${protocol}`} checked={values.protocols.includes(protocol)} onCheckedChange={(checked) => toggleProtocol(protocol, checked === true)} /><FieldLabel htmlFor={`api-route-protocol-${protocol}`}>{protocol}</FieldLabel></Field>)}</FieldGroup>{validationErrors.includes("protocolRequired") ? <FieldError>{t("protocolRequired")}</FieldError> : null}</FieldSet>
    <FieldSet data-invalid={validationErrors.includes("allowedMethodsRequired") || undefined}><FieldLegend variant="label">{t("allowedMethods")}</FieldLegend><FieldDescription>{t("allMethodsDescription")}</FieldDescription><ToggleGroup type="single" value={values.methodMode} onValueChange={(methodMode) => methodMode && onChange({ methodMode: methodMode as RouteFormValues["methodMode"], allowedMethods: methodMode === "all" ? [] : values.allowedMethods })}><ToggleGroupItem value="all">{t("allMethodsShort")}</ToggleGroupItem><ToggleGroupItem value="selected">{t("selectedMethodsShort")}</ToggleGroupItem></ToggleGroup>{values.methodMode === "selected" ? <FieldGroup className="grid grid-cols-2 gap-3 sm:grid-cols-4">{STANDARD_ROUTE_HTTP_METHODS.map((method) => <Field key={method} orientation="horizontal"><Checkbox id={`api-route-method-${method}`} checked={values.allowedMethods.includes(method)} onCheckedChange={(checked) => toggleMethod(method, checked === true)} /><FieldLabel htmlFor={`api-route-method-${method}`}>{method}</FieldLabel></Field>)}</FieldGroup> : null}{validationErrors.includes("allowedMethodsRequired") ? <FieldError>{t("allowedMethodsRequired")}</FieldError> : null}</FieldSet>
    <Field orientation="horizontal"><Checkbox id="api-route-forward-subpath" checked={values.forwardSubpath} onCheckedChange={(forwardSubpath) => onChange({ forwardSubpath: forwardSubpath === true })} /><FieldLabel htmlFor="api-route-forward-subpath">{t("forwardSubpath")}</FieldLabel></Field>
    {values.protocols.includes("websocket") ? <Field><FieldLabel htmlFor="api-route-websocket-subprotocols">{t("websocketSubprotocols")}</FieldLabel><Input id="api-route-websocket-subprotocols" value={subprotocolText} onChange={(event) => { const text = event.target.value; const websocketSubprotocols = [...new Set(text.split(",").map((item) => item.trim()).filter(Boolean))]; setSubprotocolState({ key: websocketSubprotocols.join("\u0000"), text }); onChange({ websocketSubprotocols }); }} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} /></Field> : null}
    <Field orientation="horizontal"><Switch id="api-route-enabled" checked={values.enabled} onCheckedChange={(enabled) => onChange({ enabled })} /><FieldLabel htmlFor="api-route-enabled">{t("enabled")}</FieldLabel></Field>
  </FieldGroup></FieldSet>;
}
