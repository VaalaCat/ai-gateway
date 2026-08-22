"use client";

import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { OpenAPIOperationEditor, OPENAPI_HTTP_METHODS } from "./openapi-operation-editor";
import { OpenAPIParameterEditor } from "./openapi-parameter-editor";
import { useOpenAPIEditorFieldState } from "./openapi-json-field";
import type { JSONValue, OpenAPIPathItem } from "./openapi-editor-state";

export function OpenAPIPathEditor({ routeID, path, item, finalURL, snapshotKey, disabled, onRename, onOperationAdd, onOperationRename, onOperationChange, onOperationRemove, onPathParametersChange, onValidityChange }: {
  routeID: number; path: string; item: OpenAPIPathItem; finalURL: string; snapshotKey: string; disabled: boolean;
  onRename: (value: string) => void; onOperationAdd: (method: string) => void; onOperationRename: (oldMethod: string, nextMethod: string) => void;
  onOperationChange: (method: string, operation: Record<string, JSONValue>) => void; onOperationRemove: (method: string) => void;
  onPathParametersChange: (parameters: Array<Record<string, JSONValue>>) => void;
  onValidityChange: (fieldKey: string, valid: boolean) => void;
}) {
  const t = useTranslations("apiServices");
  const editorState = useOpenAPIEditorFieldState();
  const fieldKey = `route:${routeID}:path:${path}:name`;
  const [rawPath, setRawPath] = useState(path);
  const [pathError, setPathError] = useState<string>();
  const operations = item.operations ?? {};
  const methods = Object.keys(operations).sort();
  const availableMethods = OPENAPI_HTTP_METHODS.filter((method) => !methods.includes(method));
  const [requestedMethod, setRequestedMethod] = useState(availableMethods[0] ?? "");
  const addMethod = availableMethods.includes(requestedMethod) ? requestedMethod : availableMethods[0] ?? "";
  useEffect(() => () => onValidityChange(fieldKey, true), [fieldKey, onValidityChange]);
  const validateRaw = (value: string) => {
    const error = value.trim().startsWith("/") ? undefined : "openAPIPathMustStartWithSlash";
    setPathError(error);
    onValidityChange(fieldKey, error === undefined);
    return !error;
  };
  const commitPath = () => {
    if (!validateRaw(rawPath)) return;
    try {
      onRename(rawPath);
      editorState?.movePrefix(`route:${routeID}:path:${path}`, `route:${routeID}:path:${rawPath.trim()}`);
      setPathError(undefined); onValidityChange(fieldKey, true);
    }
    catch (reason) {
      const code = reason instanceof Error ? reason.message : "openAPIPathInvalid";
      setPathError(code); onValidityChange(fieldKey, false);
    }
  };
  const pathParameters = item.parameters ?? [];
  return <Card data-testid={`openapi-path-${path}`}><CardHeader><CardTitle className="font-mono text-sm">{path}</CardTitle><CardDescription><span className="block">{t("openAPIFinalPublicURL")}</span><code className="block break-all text-xs">{finalURL}</code></CardDescription></CardHeader><CardContent className="flex min-w-0 flex-col gap-5">
    <Field data-invalid={pathError ? true : undefined}><FieldLabel htmlFor={`path-${routeID}-${path}`}>{t("openAPIPathLabel")}</FieldLabel><Input id={`path-${routeID}-${path}`} value={rawPath} disabled={disabled} aria-invalid={pathError ? true : undefined} onChange={(event) => { setRawPath(event.target.value); validateRaw(event.target.value); }} onBlur={commitPath} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); commitPath(); } }} />{pathError ? <FieldError>{t(pathError)}</FieldError> : null}</Field>
    <OpenAPIParameterEditor scope="path" fieldPrefix={`route:${routeID}:path:${path}`} snapshotKey={snapshotKey} parameters={pathParameters} disabled={disabled} onAdd={() => onPathParametersChange([...pathParameters, { name: "", in: "query", required: false, schema: {} }])} onChange={(index, parameter) => onPathParametersChange(pathParameters.map((current, currentIndex) => currentIndex === index ? parameter : current))} onRemove={(index) => onPathParametersChange(pathParameters.filter((_, currentIndex) => currentIndex !== index))} />
    {Object.entries(operations).map(([method, operation]) => <OpenAPIOperationEditor key={method} path={path} method={method} existingMethods={methods} operation={operation} snapshotKey={snapshotKey} fieldPrefix={`route:${routeID}:path:${path}:operation:${method}`} disabled={disabled} onMethodChange={(nextMethod) => onOperationRename(method, nextMethod)} onChange={(next) => onOperationChange(method, next)} onRemove={() => onOperationRemove(method)} />)}
  </CardContent><CardFooter className="flex flex-wrap gap-2"><Select value={addMethod} disabled={disabled || availableMethods.length === 0} onValueChange={setRequestedMethod}><SelectTrigger aria-label={t("openAPIAddMethodLabel")}><SelectValue placeholder={t("openAPINoMethodsAvailable")} /></SelectTrigger><SelectContent><SelectGroup>{availableMethods.map((method) => <SelectItem key={method} value={method}>{method}</SelectItem>)}</SelectGroup></SelectContent></Select><Button type="button" variant="outline" disabled={disabled || !addMethod} onClick={() => onOperationAdd(addMethod)}><Plus data-icon="inline-start" />{t("openAPIAddOperation")}</Button></CardFooter></Card>;
}
