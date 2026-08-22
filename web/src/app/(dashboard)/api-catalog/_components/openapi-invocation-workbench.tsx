"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslations } from "next-intl";
import { Copy, KeyRound, Send, X } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { APICatalogRoute } from "@/lib/api/api-access";
import type { Token } from "@/lib/types";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { buildCanonicalCurlInvocationCommand } from "../../api-services/_components/invocation-command";
import {
  createOpenAPIInvocationDraft,
  isOpenAPIMethodAllowed,
  normalizeOpenAPIParameters,
  normalizeOpenAPIRequestBody,
  type NormalizedOpenAPIParameter,
  type OpenAPIInvocationDraft,
  type OpenAPIObject,
  type OpenAPIParameterLocation,
} from "./openapi-operation-contract";
import { readBoundedOpenAPIResponse } from "./openapi-response-reader";
import {
  buildOpenAPIInvocationURL,
  type OpenAPIInvocationValues,
  type OpenAPIOperation,
} from "./openapi-operation-selection";

interface InvocationResult {
  status: number;
  body: string;
  truncated: boolean;
}

function displayResponseBody(raw: string, truncated: boolean) {
  if (truncated) return raw;
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function identity(operation: OpenAPIOperation) {
  return `${operation.routeID}:${operation.path}:${operation.method}`;
}

function draftMap(draft: OpenAPIInvocationDraft, location: OpenAPIParameterLocation) {
  return location === "header" ? draft.headers : draft[location];
}

function requestHeaders(draft: OpenAPIInvocationDraft) {
  const headers = Object.fromEntries(Object.entries(draft.headers).filter(([, value]) => value !== ""));
  if (draft.contentType && draft.body) headers["Content-Type"] = draft.contentType;
  return headers;
}

export interface OpenAPIInvocationWorkbenchProps {
  scopeKey: string;
  origin: string;
  serviceSlug: string;
  operation: OpenAPIOperation;
  route: Pick<APICatalogRoute, "allowed_methods">;
  components?: OpenAPIObject;
  token?: Pick<Token, "id" | "name" | "key">;
  tokenChecking: boolean;
  tokenFailure: "miss" | "error" | null;
  onChooseToken: () => void;
  onTokenCommandCopied: () => void;
}

export function OpenAPIInvocationWorkbench(props: OpenAPIInvocationWorkbenchProps) {
  return <OpenAPIInvocationWorkbenchState key={`${props.scopeKey}:${identity(props.operation)}`} {...props} />;
}

export function abortActiveOpenAPIRequest(active: { current: AbortController | undefined }) {
  const controller = active.current;
  active.current = undefined;
  controller?.abort();
}

function OpenAPIInvocationWorkbenchState(props: OpenAPIInvocationWorkbenchProps) {
  const t = useTranslations("apiCatalog");
  const components = useMemo(() => props.components ?? {}, [props.components]);
  const parameters = useMemo(
    () => normalizeOpenAPIParameters(props.operation, components),
    [components, props.operation],
  );
  const requestBody = useMemo(
    () => normalizeOpenAPIRequestBody(props.operation, components),
    [components, props.operation],
  );
  const [draft, setDraft] = useState(() => createOpenAPIInvocationDraft(props.operation, components));
  const [result, setResult] = useState<InvocationResult>();
  const [failure, setFailure] = useState<string>();
  const [sending, setSending] = useState(false);
  const activeController = useRef<AbortController | undefined>(undefined);

  useEffect(() => () => {
    abortActiveOpenAPIRequest(activeController);
  }, []);

  const values: OpenAPIInvocationValues = useMemo(() => ({
    ...draft.path,
    query: Object.fromEntries(Object.entries(draft.query).filter(([, value]) => value !== "")),
    headers: requestHeaders(draft),
  }), [draft]);
  const url = buildOpenAPIInvocationURL(
    props.origin,
    props.serviceSlug,
    props.operation.routeSlug,
    props.operation.path,
    values,
  );
  const missingParameters = parameters.filter((parameter) => (
    parameter.required
    && parameter.in !== "unknown"
    && draftMap(draft, parameter.in)[parameter.name]?.trim() === ""
  ));
  const bodyMissing = requestBody.required && draft.body.trim() === "";
  const selectedMediaType = requestBody.mediaTypes.find((media) => media.contentType === draft.contentType);
  const unsupported = parameters.some((parameter) => !parameter.supported)
    || !requestBody.supported
    || selectedMediaType?.supported === false;
  const methodAllowed = isOpenAPIMethodAllowed(props.route.allowed_methods, props.operation.method);
  const canSend = Boolean(
    url
    && props.token?.key
    && !props.tokenChecking
    && !props.tokenFailure
    && missingParameters.length === 0
    && !bodyMissing
    && !unsupported
    && methodAllowed,
  );
  const headers = requestHeaders(draft);
  const command = props.origin ? buildCanonicalCurlInvocationCommand({
    url,
    method: props.operation.method,
    headers,
    body: draft.body,
    token: "${API_TOKEN}",
  }) : "";

  const update = useCallback((location: OpenAPIParameterLocation, name: string, value: string) => {
    setDraft((current) => ({
      ...current,
      [location === "header" ? "headers" : location]: {
        ...draftMap(current, location),
        [name]: value,
      },
    }));
  }, []);
  const selectContentType = (contentType: string) => {
    const media = requestBody.mediaTypes.find((item) => item.contentType === contentType);
    if (!media) return;
    setDraft((current) => ({ ...current, contentType, body: media.body }));
  };
  const copyCommand = () => {
    if (!canSend) return;
    return copyTextWithFeedback(command, {
      success: t("templateCopied"),
      error: t("copyFailed"),
    });
  };
  const cancel = () => {
    activeController.current?.abort();
  };
  const send = async () => {
    if (!canSend || sending || !props.token?.key) return;
    const controller = new AbortController();
    activeController.current?.abort();
    activeController.current = controller;
    setSending(true);
    setResult(undefined);
    setFailure(undefined);
    try {
      const response = await fetch(url, {
        method: props.operation.method,
        signal: controller.signal,
        headers: { ...headers, Authorization: `Bearer ${props.token.key}` },
        ...(draft.body ? { body: draft.body } : {}),
      });
      const bounded = await readBoundedOpenAPIResponse(response, controller.signal);
      if (activeController.current !== controller || controller.signal.aborted) return;
      setResult({
        status: response.status,
        body: displayResponseBody(bounded.body, bounded.truncated),
        truncated: bounded.truncated,
      });
    } catch (error) {
      if (activeController.current !== controller || controller.signal.aborted) return;
      setFailure(error instanceof Error ? error.message : t("openAPIRequestFailed"));
    } finally {
      if (activeController.current === controller) {
        activeController.current = undefined;
        setSending(false);
      }
    }
  };

  return (
    <Card data-testid="openapi-invocation-workbench">
      <CardHeader className="gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <CardTitle className="text-base">{t("openAPIInvocationTitle")}</CardTitle>
          <Badge variant="secondary" className="font-mono">{props.operation.method}</Badge>
        </div>
        <div className="flex min-w-0 flex-col gap-1 rounded-md border border-border bg-muted/50 p-3">
          <span className="text-xs font-medium text-muted-foreground">{t("openAPIRequestURL")}</span>
          <code className="break-all font-mono text-sm">{url}</code>
        </div>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-5">
        {!methodAllowed ? <InvocationWarning title={t("openAPIMethodMismatch")} /> : null}
        {unsupported ? <InvocationWarning title={t("openAPIInvocationUnsupported")} /> : null}
        <InvocationFields
          parameters={parameters}
          requestBody={requestBody}
          draft={draft}
          onUpdate={update}
          onBodyChange={(body) => setDraft((current) => ({ ...current, body }))}
          onContentTypeChange={selectContentType}
        />
        <pre className="max-h-48 overflow-auto rounded-md border border-border bg-muted/50 p-3 font-mono text-xs"><code>{command}</code></pre>
        <div className="flex flex-wrap gap-2">
          {sending ? (
            <Button type="button" size="sm" variant="destructive" onClick={cancel}>
              <X data-icon="inline-start" />{t("cancelOpenAPIRequest")}
            </Button>
          ) : (
            <Button type="button" size="sm" disabled={!canSend} onClick={() => void send()}>
              <Send data-icon="inline-start" />{t("sendOpenAPIRequest")}
            </Button>
          )}
          <Button type="button" variant="outline" size="sm" disabled={!canSend} onClick={() => void copyCommand()}>
            <Copy data-icon="inline-start" />{t("copyTemplate")}
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={props.onChooseToken}>
            <KeyRound data-icon="inline-start" />{t("changeToken")}
          </Button>
        </div>
        {failure ? <Alert variant="destructive"><AlertTitle>{t("openAPIRequestFailed")}</AlertTitle><AlertDescription>{failure}</AlertDescription></Alert> : null}
        {result ? (
          <section data-testid="openapi-invocation-result" className="flex min-w-0 flex-col gap-2">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold">{t("openAPIResponse")}</h3>
              <Badge variant="outline">{result.status}</Badge>
              {result.truncated ? <Badge variant="secondary">{t("openAPIResponseTruncated")}</Badge> : null}
            </div>
            <pre className="max-h-80 overflow-auto rounded-md border border-border bg-muted/50 p-3 font-mono text-xs"><code>{result.body}</code></pre>
          </section>
        ) : null}
      </CardContent>
    </Card>
  );
}

function InvocationWarning({ title }: { title: string }) {
  return <Alert><AlertTitle>{title}</AlertTitle></Alert>;
}

function InvocationFields({
  parameters,
  requestBody,
  draft,
  onUpdate,
  onBodyChange,
  onContentTypeChange,
}: {
  parameters: NormalizedOpenAPIParameter[];
  requestBody: ReturnType<typeof normalizeOpenAPIRequestBody>;
  draft: OpenAPIInvocationDraft;
  onUpdate: (location: OpenAPIParameterLocation, name: string, value: string) => void;
  onBodyChange: (body: string) => void;
  onContentTypeChange: (contentType: string) => void;
}) {
  const t = useTranslations("apiCatalog");
  return (
    <FieldGroup className="gap-4">
      {(["path", "query", "header"] as const).map((location) => {
        const items = parameters.filter((parameter) => parameter.in === location);
        return items.length ? (
          <section key={location} className="flex flex-col gap-3">
            <h3 className="text-sm font-semibold">{t(location === "path" ? "pathParameters" : location === "query" ? "query" : "headers")}</h3>
            <FieldGroup className="gap-3">
              {items.map((parameter, index) => {
                const value = draftMap(draft, location)[parameter.name] ?? "";
                const missing = parameter.required && value.trim() === "";
                const id = `openapi-${location}-${index}`;
                return (
                  <Field key={`${location}:${parameter.name}`} data-invalid={missing || !parameter.supported}>
                    <FieldLabel htmlFor={id}>{parameter.name}</FieldLabel>
                    <Input
                      id={id}
                      aria-invalid={missing || !parameter.supported}
                      value={value}
                      onChange={(event) => onUpdate(location, parameter.name, event.target.value)}
                    />
                    {missing ? <FieldError>{t("openAPIRequiredField")}</FieldError> : null}
                  </Field>
                );
              })}
            </FieldGroup>
          </section>
        ) : null;
      })}
      {requestBody.mediaTypes.length ? (
        <FieldGroup className="gap-3">
          {requestBody.mediaTypes.length > 1 ? (
            <Field>
              <FieldLabel htmlFor="openapi-content-type">{t("contentType")}</FieldLabel>
              <Select value={draft.contentType} onValueChange={onContentTypeChange}>
                <SelectTrigger id="openapi-content-type" aria-label={t("contentType")} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {requestBody.mediaTypes.map((media) => <SelectItem key={media.contentType} value={media.contentType}>{media.contentType}</SelectItem>)}
                </SelectContent>
              </Select>
            </Field>
          ) : null}
          <Field data-invalid={requestBody.required && draft.body.trim() === ""}>
            <FieldLabel htmlFor="openapi-body">{t("body")}</FieldLabel>
            <Textarea
              id="openapi-body"
              rows={6}
              aria-invalid={requestBody.required && draft.body.trim() === ""}
              value={draft.body}
              onChange={(event) => onBodyChange(event.target.value)}
            />
            {requestBody.required && draft.body.trim() === "" ? <FieldError>{t("openAPIRequiredField")}</FieldError> : null}
          </Field>
        </FieldGroup>
      ) : null}
    </FieldGroup>
  );
}
