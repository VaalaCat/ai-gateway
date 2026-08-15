"use client";

import { useState } from "react";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { APIRouteTargetCommand, APIUpstreamAuthType } from "@/lib/api/api-services";
import { UpstreamCredentialFields } from "../upstream-credential-fields";
import { UpstreamHeaderFields } from "../upstream-header-fields";
import type { HeaderOverrideRow } from "../route-form/route-form-state";

const authTypes: APIUpstreamAuthType[] = ["none", "bearer", "header", "query", "basic"];

interface RouteTargetPickerProps {
  target: APIRouteTargetCommand;
  headerRows: HeaderOverrideRow[];
  serviceId: number;
  onTargetChange: (target: APIRouteTargetCommand) => void;
  onHeaderRowsChange: (rows: HeaderOverrideRow[]) => void;
  validationErrors?: string[];
  existingTargetLookup?: {
    state: "loading" | "ready" | "missing" | "error" | "service-mismatch";
    retry: () => Promise<unknown>;
  };
  t: (key: string, values?: Record<string, string | number | Date>) => string;
}

function emptyCreatedTarget(): Extract<APIRouteTargetCommand, { mode: "create" }> {
  return { mode: "create", backend: { name: "" }, first_upstream: { name: "", base_url: "", priority: 0, weight: 1, status: 1, auth_type: "none" } };
}

function CreatedTargetFields({ target, headerRows, validationErrors, onChange, onHeaderRowsChange, t }: { target: Extract<APIRouteTargetCommand, { mode: "create" }>; headerRows: HeaderOverrideRow[]; validationErrors: string[]; onChange: RouteTargetPickerProps["onTargetChange"]; onHeaderRowsChange: RouteTargetPickerProps["onHeaderRowsChange"]; t: RouteTargetPickerProps["t"] }) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const patchEndpoint = (patch: Partial<typeof target.first_upstream>) => onChange({ ...target, first_upstream: { ...target.first_upstream, ...patch } });
  const error = (key: string) => validationErrors.includes(key);
  const advancedHasError = error("invalidProxyUrl") || error("invalidHeaderOverride");
  return <FieldGroup>
    <Field data-invalid={error("invalidTargetName") || undefined}><FieldLabel htmlFor="api-route-target-name">{t("targetName")}</FieldLabel><Input id="api-route-target-name" aria-invalid={error("invalidTargetName")} value={target.backend.name} onChange={(event) => onChange({ ...target, backend: { name: event.target.value } })} />{error("invalidTargetName") ? <FieldError>{t("invalidTargetName")}</FieldError> : null}</Field>
    <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2"><Field data-invalid={error("endpointNameRequired") || undefined}><FieldLabel htmlFor="api-route-endpoint-name">{t("endpointName")}</FieldLabel><Input id="api-route-endpoint-name" aria-invalid={error("endpointNameRequired")} value={target.first_upstream.name} onChange={(event) => patchEndpoint({ name: event.target.value })} />{error("endpointNameRequired") ? <FieldError>{t("endpointNameRequired")}</FieldError> : null}</Field><Field data-invalid={error("endpointUrlRequired") || error("invalidEndpointUrl") || undefined}><FieldLabel htmlFor="api-route-endpoint-url">{t("endpointUrl")}</FieldLabel><Input id="api-route-endpoint-url" type="url" aria-invalid={error("endpointUrlRequired") || error("invalidEndpointUrl")} value={target.first_upstream.base_url} onChange={(event) => patchEndpoint({ base_url: event.target.value })} />{error("endpointUrlRequired") ? <FieldError>{t("endpointUrlRequired")}</FieldError> : null}{error("invalidEndpointUrl") ? <FieldError>{t("invalidEndpointUrl")}</FieldError> : null}</Field></FieldGroup>
    <Field><FieldLabel htmlFor="api-route-target-auth">{t("authType")}</FieldLabel><Select value={target.first_upstream.auth_type} onValueChange={(authType) => patchEndpoint({ auth_type: authType as APIUpstreamAuthType, credential: undefined })}><SelectTrigger id="api-route-target-auth"><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{authTypes.map((type) => <SelectItem key={type} value={type}>{type}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>
    <UpstreamCredentialFields idPrefix="api-route-target" authType={target.first_upstream.auth_type} credential={target.first_upstream.credential} onChange={(credential) => patchEndpoint({ credential })} error={error("credentialRequired") ? t("credentialRequired") : undefined} />
    <Collapsible open={advancedOpen || advancedHasError} onOpenChange={setAdvancedOpen}><CollapsibleTrigger asChild><Button type="button" variant="outline">{t("advancedTarget")}</Button></CollapsibleTrigger><CollapsibleContent><FieldGroup className="pt-4"><Field data-invalid={error("invalidProxyUrl") || undefined}><FieldLabel htmlFor="api-route-target-proxy">{t("proxyUrl")}</FieldLabel><Input id="api-route-target-proxy" type="url" aria-invalid={error("invalidProxyUrl")} value={target.first_upstream.proxy_url ?? ""} onChange={(event) => patchEndpoint({ proxy_url: event.target.value })} />{error("invalidProxyUrl") ? <FieldError>{t("invalidProxyUrl")}</FieldError> : null}</Field><UpstreamHeaderFields idPrefix="api-route-target" rows={headerRows} onChange={onHeaderRowsChange} t={t} /></FieldGroup></CollapsibleContent></Collapsible>
  </FieldGroup>;
}

export function RouteTargetPicker({ target, headerRows, serviceId, onTargetChange, onHeaderRowsChange, validationErrors = [], existingTargetLookup, t }: RouteTargetPickerProps) {
  const creating = target.mode === "create";
  const selectedTargetTrusted = existingTargetLookup?.state !== "missing" && existingTargetLookup?.state !== "service-mismatch";
  return <FieldSet><FieldLegend>{t("target")}</FieldLegend><FieldGroup>
    <ToggleGroup type="single" value={creating ? "create" : "existing"} onValueChange={(value) => { if (value === "create") onTargetChange(emptyCreatedTarget()); if (value === "existing") onTargetChange({ mode: "existing", backend_id: 0 }); }}><ToggleGroupItem value="existing">{t("targetExisting")}</ToggleGroupItem><ToggleGroupItem value="create">{t("targetCreate")}</ToggleGroupItem></ToggleGroup>
    {target.mode === "existing" ? <Field data-invalid={validationErrors.includes("backendRequired") || existingTargetLookup?.state === "missing" || existingTargetLookup?.state === "error" || existingTargetLookup?.state === "service-mismatch" || undefined}><FieldLabel htmlFor="api-route-backend">{t("backend")}</FieldLabel><EntityPicker id="api-route-backend" entity="api-backend" apiServiceId={serviceId} value={target.backend_id > 0 && selectedTargetTrusted ? String(target.backend_id) : ""} onChange={(backendID) => onTargetChange({ mode: "existing", backend_id: Number(backendID) })} /><FieldDescription>{t("targetExistingDescription")}</FieldDescription>{validationErrors.includes("backendRequired") ? <FieldError>{t("backendRequired")}</FieldError> : null}{existingTargetLookup?.state === "missing" ? <FieldError>{t("targetNotFound")}</FieldError> : null}{existingTargetLookup?.state === "service-mismatch" ? <FieldError>{t("targetServiceMismatch")}</FieldError> : null}{existingTargetLookup?.state === "error" ? <Alert variant="destructive"><AlertTitle>{t("targetLoadFailed")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-2"><span>{t("loadFailedDescription")}</span><Button type="button" variant="outline" size="sm" onClick={() => void existingTargetLookup.retry()}>{t("retry")}</Button></AlertDescription></Alert> : null}</Field> : <CreatedTargetFields target={target} headerRows={headerRows} validationErrors={validationErrors} onChange={onTargetChange} onHeaderRowsChange={onHeaderRowsChange} t={t} />}
  </FieldGroup></FieldSet>;
}
