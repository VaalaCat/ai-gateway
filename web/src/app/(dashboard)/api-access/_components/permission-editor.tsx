"use client";

import { useEffect, useMemo, type MutableRefObject } from "react";
import { useTranslations } from "next-intl";
import { Plus, Trash2 } from "lucide-react";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useAPIRoute } from "@/lib/api/api-services";
import type { APIPermission, APIResource } from "@/lib/api/api-access";

export interface PermissionDraft extends APIPermission {
  rowKey: number;
  scope: "all" | "specific";
  apiServiceId?: number;
}

interface PermissionTargetConfig { entity: "api-service" | "api-route"; needsService?: boolean; }

const permissionTargets: Record<APIResource, PermissionTargetConfig> = {
  api_service: { entity: "api-service" },
  api_route: { entity: "api-route", needsService: true },
};
const resources: APIResource[] = ["api_service", "api_route"];

interface PermissionEditorProps {
  rows: PermissionDraft[];
  onChange: (rows: PermissionDraft[]) => void;
  onAdd: () => void;
  resolvedServiceIDs: MutableRefObject<Map<number, number>>;
}

export function PermissionEditor({ rows, onChange, onAdd, resolvedServiceIDs }: PermissionEditorProps) {
  const t = useTranslations("apiAccess");
  const patch = (rowKey: number, update: Partial<PermissionDraft>) => onChange(rows.map((row) => row.rowKey === rowKey ? { ...row, ...update } : row));
  return <FieldSet>
    <div className="flex items-center justify-between gap-3"><FieldLegend variant="label">{t("permissions")}</FieldLegend><Button type="button" size="sm" variant="outline" onClick={onAdd}><Plus data-icon="inline-start" />{t("addPermission")}</Button></div>
    <FieldGroup className="gap-3">{rows.map((row) => <PermissionRow key={row.rowKey} row={row} resolvedServiceIDs={resolvedServiceIDs} onPatch={(update) => patch(row.rowKey, update)} onRemove={() => onChange(rows.filter((candidate) => candidate.rowKey !== row.rowKey))} />)}</FieldGroup>
  </FieldSet>;
}

function PermissionRow({ row, resolvedServiceIDs, onPatch, onRemove }: { row: PermissionDraft; resolvedServiceIDs: MutableRefObject<Map<number, number>>; onPatch: (update: Partial<PermissionDraft>) => void; onRemove: () => void }) {
  const t = useTranslations("apiAccess");
  const target = permissionTargets[row.resource];
  const route = useAPIRoute(row.resource === "api_route" && row.scope === "specific" ? row.resource_id : 0);
  const loadedServiceId = row.resource === "api_route" ? route.data?.api_service_id : undefined;
  const loadError = row.resource === "api_route" && row.scope === "specific" ? route.error : undefined;
  const apiServiceId = row.apiServiceId ?? loadedServiceId;
  useEffect(() => {
    const serviceIDs = resolvedServiceIDs.current;
    if (row.scope === "specific" && target.needsService && loadedServiceId) serviceIDs.set(row.rowKey, loadedServiceId);
    else serviceIDs.delete(row.rowKey);
    return () => { serviceIDs.delete(row.rowKey); };
  }, [loadedServiceId, resolvedServiceIDs, row.rowKey, row.scope, target.needsService]);
  const childDisabled = Boolean(target.needsService && !apiServiceId);
  const resourceOptions = useMemo(() => resources.map((resource) => <SelectItem key={resource} value={resource}>{t(`resourceOptions.${resource}`)}</SelectItem>), [t]);
  const setResource = (resource: APIResource) => onPatch({ resource, resource_id: 0, action: "invoke", scope: resource === "api_route" ? "specific" : "all", apiServiceId: undefined });
  const setScope = (scope: "all" | "specific") => onPatch({ scope, resource_id: 0, apiServiceId: undefined });

  return <FieldGroup className="rounded-md border p-3">
    <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-end">
      <Field><FieldLabel htmlFor={`api-permission-resource-${row.rowKey}`}>{t("resource")}</FieldLabel><Select value={row.resource} onValueChange={(resource) => setResource(resource as APIResource)}><SelectTrigger id={`api-permission-resource-${row.rowKey}`} aria-label={t("resource")}><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{resourceOptions}</SelectGroup></SelectContent></Select></Field>
      <Button type="button" size="icon-sm" variant="ghost" aria-label={t("removePermission")} onClick={onRemove}><Trash2 /></Button>
    </div>
    {row.resource === "api_service" ? <Field>
      <FieldLabel>{t("scope")}</FieldLabel>
      <ToggleGroup type="single" value={row.scope} onValueChange={(value) => { if (value === "all" || value === "specific") setScope(value); }} aria-label={t("scope")}>
        <ToggleGroupItem value="all">{t("allResources")}</ToggleGroupItem><ToggleGroupItem value="specific">{t("specificResource")}</ToggleGroupItem>
      </ToggleGroup>
    </Field> : null}
    {row.scope === "specific" ? <PermissionTargetFields row={row} target={target} apiServiceId={apiServiceId} childDisabled={childDisabled} loadError={loadError} onPatch={onPatch} /> : null}
  </FieldGroup>;
}

function PermissionTargetFields({ row, target, apiServiceId, childDisabled, loadError, onPatch }: { row: PermissionDraft; target: PermissionTargetConfig; apiServiceId?: number; childDisabled: boolean; loadError: unknown; onPatch: (update: Partial<PermissionDraft>) => void }) {
  const t = useTranslations("apiAccess");
  if (loadError) return <Alert variant="destructive"><AlertTitle>{t("resourceLoadFailed")}</AlertTitle><AlertDescription>{t("resourceLoadFailedDescription")}</AlertDescription></Alert>;
  if (!target.needsService) return <Field><FieldLabel htmlFor={`api-permission-target-${row.rowKey}`}>{t("specificResource")}</FieldLabel><EntityPicker id={`api-permission-target-${row.rowKey}`} entity={target.entity} value={String(row.resource_id || "")} onChange={(value) => onPatch({ resource_id: Number(value) || 0 })} /></Field>;
  return <FieldGroup className="grid gap-3 md:grid-cols-2">
    <Field><FieldLabel htmlFor={`api-permission-service-${row.rowKey}`}>{t("service")}</FieldLabel><EntityPicker id={`api-permission-service-${row.rowKey}`} entity="api-service" value={apiServiceId ? String(apiServiceId) : ""} onChange={(value) => onPatch({ apiServiceId: Number(value) || undefined, resource_id: 0 })} /></Field>
    <Field><FieldLabel htmlFor={`api-permission-target-${row.rowKey}`}>{t("specificResource")}</FieldLabel><EntityPicker id={`api-permission-target-${row.rowKey}`} entity={target.entity} apiServiceId={apiServiceId} disabled={childDisabled} value={String(row.resource_id || "")} onChange={(value) => onPatch({ resource_id: Number(value) || 0 })} /></Field>
  </FieldGroup>;
}
