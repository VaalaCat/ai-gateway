"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";

import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { OpenAPIEditorFieldStateProvider, OpenAPIJSONField } from "./openapi-json-field";
import { OpenAPIPathEditor } from "./openapi-path-editor";
import { addOperation, exportedPath, removeOperation, renameOperation, renamePath, upsertOperation, type JSONValue, type OpenAPIDocumentSnapshot, type OpenAPIPathItem } from "./openapi-editor-state";

function publicURL(origin: string, serviceSlug: string, path: string) {
  const base = origin ? `${origin}/v1/api/${serviceSlug}` : `/v1/api/${serviceSlug}`;
  return path === "/" ? base : `${base}${path}`;
}

function stringValue(value: JSONValue | undefined) { return typeof value === "string" ? value : ""; }

export function openAPISnapshotKey(snapshot: OpenAPIDocumentSnapshot) {
  return `${snapshot.service.id}:${snapshot.service.updated_at}:${snapshot.routes.map((route) => `${route.id}:${route.updated_at}`).join(",")}`;
}

interface OpenAPIDocumentEditorProps {
  snapshot: OpenAPIDocumentSnapshot; origin: string; disabled: boolean;
  onChange: (snapshot: OpenAPIDocumentSnapshot) => void; onValidityChange?: (valid: boolean) => void;
}

export function OpenAPIDocumentEditor(props: OpenAPIDocumentEditorProps) {
  const snapshotKey = openAPISnapshotKey(props.snapshot);
  return <OpenAPIDocumentEditorState key={snapshotKey} {...props} snapshotKey={snapshotKey} />;
}

function OpenAPIDocumentEditorState({ snapshot, snapshotKey, origin, disabled, onChange, onValidityChange }: OpenAPIDocumentEditorProps & { snapshotKey: string }) {
  const t = useTranslations("apiServices");
  const [selectedRouteID, setSelectedRouteID] = useState(snapshot.routes[0]?.id);
  const [invalidFields, setInvalidFields] = useState<Set<string>>(new Set());
  const [jsonValid, setJSONValid] = useState(true);
  useEffect(() => { onValidityChange?.(invalidFields.size === 0 && jsonValid); }, [invalidFields, jsonValid, onValidityChange]);
  const selectedRoute = snapshot.routes.find((route) => route.id === selectedRouteID) ?? snapshot.routes[0];
  const routePaths = useMemo(() => selectedRoute ? Object.entries(selectedRoute.paths).sort(([a], [b]) => a.localeCompare(b)) : [], [selectedRoute]);
  const setDocument = (patch: Record<string, JSONValue>) => onChange({ ...snapshot, service: { ...snapshot.service, document: { ...snapshot.service.document, ...patch } } });
  const setFieldValidity = useCallback((fieldKey: string, valid: boolean) => setInvalidFields((current) => {
    if (valid === !current.has(fieldKey)) return current;
    const next = new Set(current);
    if (valid) next.delete(fieldKey); else next.add(fieldKey);
    return next;
  }), []);
  const updatePathItem = (routeID: number, path: string, update: (item: OpenAPIPathItem) => OpenAPIPathItem) => onChange({
    ...snapshot,
    routes: snapshot.routes.map((route) => route.id === routeID ? { ...route, paths: { ...route.paths, [path]: update(route.paths[path] ?? {}) } } : route),
  });
  return <OpenAPIEditorFieldStateProvider onValidityChange={setJSONValid}><div className="grid min-w-0 gap-4 lg:grid-cols-[15rem_minmax(0,1fr)]">
    <aside className="min-w-0"><Card><CardHeader><CardTitle>{t("openAPIRouteList")}</CardTitle><CardDescription>{t("openAPIRouteListDescription")}</CardDescription></CardHeader><CardContent><Field><FieldLabel htmlFor="openapi-route-select">{t("openAPIRouteLabel")}</FieldLabel><Select value={selectedRoute ? String(selectedRoute.id) : ""} disabled={disabled || !selectedRoute} onValueChange={(value) => setSelectedRouteID(Number(value))}><SelectTrigger id="openapi-route-select" aria-label={t("openAPIRouteLabel")} className="w-full"><SelectValue placeholder={t("openAPINoRoute")} /></SelectTrigger><SelectContent><SelectGroup>{snapshot.routes.map((route) => <SelectItem key={route.id} value={String(route.id)}>{route.slug || t("rootRoute")}</SelectItem>)}</SelectGroup></SelectContent></Select></Field></CardContent><CardFooter><span className="text-xs text-muted-foreground">{t("openAPIRouteCount", { count: snapshot.routes.length })}</span></CardFooter></Card></aside>
    <main className="flex min-w-0 flex-col gap-4">
      <Card><CardHeader><CardTitle>{snapshot.service.name}</CardTitle><CardDescription>{t("openAPIServiceFactsDescription")}</CardDescription></CardHeader><CardContent><FieldGroup><Field><FieldLabel htmlFor="openapi-version">{t("openAPIVersion")}</FieldLabel><Input id="openapi-version" value={stringValue(snapshot.service.document.openapi)} disabled={disabled} onChange={(event) => setDocument({ openapi: event.target.value })} /></Field><Field><FieldLabel htmlFor="openapi-info-version">{t("openAPIDocumentVersion")}</FieldLabel><Input id="openapi-info-version" value={stringValue((snapshot.service.document.info as Record<string, JSONValue> | undefined)?.version)} disabled={disabled} onChange={(event) => setDocument({ info: { ...(snapshot.service.document.info as Record<string, JSONValue> ?? {}), version: event.target.value } })} /></Field><OpenAPIJSONField fieldKey="service:tags" snapshotKey={snapshotKey} id="openapi-tags" label={t("openAPITags")} value={snapshot.service.document.tags} disabled={disabled} onChange={(value) => setDocument({ tags: value })} /><OpenAPIJSONField fieldKey="service:components" snapshotKey={snapshotKey} id="openapi-components" label={t("openAPIComponents")} value={snapshot.service.document.components} objectOnly disabled={disabled} onChange={(value) => setDocument({ components: value })} /></FieldGroup></CardContent><CardFooter><span className="text-xs text-muted-foreground">{t("openAPIServiceID", { id: snapshot.service.id })}</span></CardFooter></Card>
      {selectedRoute ? <section className="flex min-w-0 flex-col gap-4" aria-labelledby="openapi-paths-title"><div><h2 id="openapi-paths-title" className="text-lg font-semibold">{selectedRoute.slug || t("rootRoute")}</h2><p className="font-mono text-xs text-muted-foreground">{t("openAPIUpstreamPath", { path: selectedRoute.upstream_path || "/" })}</p></div>{routePaths.length === 0 ? <Card><CardHeader><CardTitle>{t("openAPIPathsEmpty")}</CardTitle><CardDescription>{t("openAPIPathsEmptyDescription")}</CardDescription></CardHeader><CardContent /><CardFooter /></Card> : routePaths.map(([path, item]) => <OpenAPIPathEditor key={path} routeID={selectedRoute.id} path={path} item={item} finalURL={publicURL(origin, snapshot.service.slug, exportedPath(selectedRoute, path))} snapshotKey={snapshotKey} disabled={disabled} onRename={(next) => onChange(renamePath(snapshot, selectedRoute.id, path, next))} onOperationAdd={(method) => onChange(addOperation(snapshot, selectedRoute.id, path, method))} onOperationRename={(method, nextMethod) => onChange(renameOperation(snapshot, selectedRoute.id, path, method, nextMethod))} onOperationChange={(method, operation) => onChange(upsertOperation(snapshot, selectedRoute.id, path, method, operation))} onOperationRemove={(method) => onChange(removeOperation(snapshot, selectedRoute.id, path, method))} onPathParametersChange={(parameters) => updatePathItem(selectedRoute.id, path, (current) => ({ ...current, parameters }))} onValidityChange={setFieldValidity} />)}</section> : <Card><CardHeader><CardTitle>{t("openAPIRoutesEmpty")}</CardTitle><CardDescription>{t("openAPIRoutesEmptyDescription")}</CardDescription></CardHeader><CardContent /><CardFooter /></Card>}
    </main>
  </div></OpenAPIEditorFieldStateProvider>;
}
