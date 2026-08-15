"use client";

import { useId, useState } from "react";
import { useTranslations } from "next-intl";
import { Copy, KeyRound, Plus, Trash2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";
import type { Token } from "@/lib/types";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import {
  buildInvocationCommand,
  buildInvocationCommands,
  type InvocationResult,
} from "../../api-services/_components/invocation-command";
import type { CatalogProtocol } from "./catalog-selection";
import { RouteAccessStatus } from "./route-access-status";
import {
  requestExampleFromDraft,
  type RequestExampleDraft,
} from "./request-example-draft";

export interface InvocationWorkbenchProps {
  origin: string;
  service: APICatalogService;
  route: APICatalogRoute;
  protocol: CatalogProtocol;
  draft: RequestExampleDraft;
  tokenID: number;
  token?: Pick<Token, "id" | "name" | "key">;
  tokenChecking: boolean;
  tokenFailure: "miss" | "error" | null;
  effectiveAccess: "granted" | "not_granted" | "unknown";
  effectiveUnavailable?: boolean;
  onProtocolChange: (protocol: CatalogProtocol) => void;
  onDraftChange: (draft: RequestExampleDraft) => void;
  onChooseToken: () => void;
  onTokenCommandCopied: () => void;
}

const commandKindByProtocol = {
  http: "curl",
  websocket: "websocat",
} satisfies Record<CatalogProtocol, InvocationResult["kind"]>;

function isGatewayAuthorizationHeader(name: string) {
  return name.trim().toLowerCase() === "authorization";
}

function commandForProtocol(
  props: Pick<InvocationWorkbenchProps, "origin" | "service" | "route" | "protocol" | "draft">,
  token: string,
) {
  const input = {
    origin: props.origin,
    serviceSlug: props.service.slug,
    routeSlug: props.route.slug,
    protocols: props.route.protocols,
    example: requestExampleFromDraft(props.draft, props.protocol),
    token,
  };
  return buildInvocationCommands(input).find(
    (command) => command.kind === commandKindByProtocol[props.protocol],
  ) ?? buildInvocationCommand({ ...input, protocols: [props.protocol] });
}

export function InvocationWorkbench(props: InvocationWorkbenchProps) {
  const t = useTranslations("apiCatalog");
  const example = requestExampleFromDraft(props.draft, props.protocol);
  const templateCommand = props.origin
    ? commandForProtocol(props, "${API_TOKEN}")
    : { kind: commandKindByProtocol[props.protocol], publicUrl: "", command: "" };
  const supportsMultipleProtocols = props.route.protocols.length > 1;
  const canCopyReal = Boolean(
    props.token?.key
      && !props.tokenChecking
      && !props.tokenFailure,
  );

  const copyTemplate = () => copyTextWithFeedback(templateCommand.command, {
    success: t("templateCopied"),
    error: t("copyFailed"),
  });
  const copyWithToken = async () => {
    if (!canCopyReal || !props.token?.key) return;
    const command = commandForProtocol(props, props.token.key);
    const copied = await copyTextWithFeedback(command.command, {
      success: t("commandCopied", { name: props.token.name }),
      error: t("copyFailed"),
    });
    if (copied) props.onTokenCommandCopied();
    return copied;
  };

  return (
    <section
      data-testid="invocation-workbench"
      className="flex min-w-0 flex-col gap-4"
      aria-labelledby="invocation-workbench-title"
      data-effective-access={props.effectiveAccess}
    >
      <WorkbenchHeading service={props.service} route={props.route} />
      {supportsMultipleProtocols ? (
        <ProtocolToggle
          value={props.protocol}
          supported={props.route.protocols}
          onChange={props.onProtocolChange}
        />
      ) : null}
      <PublicRequestLine method={example.method} url={templateCommand.publicUrl} />
      <RequestExampleFields
        key={props.route.id}
        draft={props.draft}
        protocol={props.protocol}
        onChange={props.onDraftChange}
      />
      <CommandPreview command={templateCommand.command} />
      <InvocationActions
        canCopyReal={canCopyReal}
        onChooseToken={props.onChooseToken}
        onCopyTemplate={() => void copyTemplate()}
        onCopyWithToken={() => void copyWithToken()}
      />
      <RouteAccessStatus
        tokenID={props.tokenID}
        token={props.token}
        isChecking={props.tokenChecking}
        failure={props.tokenFailure}
        effectiveAccess={props.effectiveAccess}
        effectiveUnavailable={props.effectiveUnavailable}
      />
    </section>
  );
}

function WorkbenchHeading({ service, route }: { service: APICatalogService; route: APICatalogRoute }) {
  const t = useTranslations("apiCatalog");
  return (
    <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <p className="text-xs font-medium text-muted-foreground">{t("invocationPreviewTitle")}</p>
        <h3 id="invocation-workbench-title" className="text-lg font-semibold tracking-tight">
          <span>{service.name}</span>
          <span className="text-muted-foreground"> / </span>
          <span className="font-mono">{route.slug}</span>
        </h3>
      </div>
      <div className="flex flex-wrap gap-1">
        {route.protocols.map((protocol) => <Badge key={protocol} variant="outline">{protocol}</Badge>)}
        {(route.allowed_methods.length ? route.allowed_methods : [t("allMethods")]).map((method) => (
          <Badge key={method} variant="secondary">{method}</Badge>
        ))}
      </div>
    </div>
  );
}

function ProtocolToggle({
  value,
  supported,
  onChange,
}: {
  value: CatalogProtocol;
  supported: CatalogProtocol[];
  onChange: (protocol: CatalogProtocol) => void;
}) {
  const t = useTranslations("apiCatalog");
  return (
    <Field>
      <FieldLabel>{t("invocationProtocol")}</FieldLabel>
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        value={value}
        aria-label={t("invocationProtocol")}
        onValueChange={(next) => {
          if (next === "http" || next === "websocket") onChange(next);
        }}
      >
        {supported.map((protocol) => (
          <ToggleGroupItem
            key={protocol}
            value={protocol}
            aria-label={t(protocol === "http" ? "httpProtocol" : "websocketProtocol")}
          >
            {t(protocol === "http" ? "httpProtocol" : "websocketProtocol")}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </Field>
  );
}

function PublicRequestLine({ method, url }: { method: string; url: string }) {
  const t = useTranslations("apiServices");
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border border-border bg-muted/50 p-3">
      <span className="text-xs font-medium text-muted-foreground">{t("clientRequest")}</span>
      <div className="flex min-w-0 flex-wrap items-baseline gap-2 font-mono text-sm">
        <span className="font-semibold">{method || "GET"}</span>
        <span className="break-all">{url}</span>
      </div>
    </div>
  );
}

function RequestExampleFields({
  draft,
  protocol,
  onChange,
}: {
  draft: RequestExampleDraft;
  protocol: CatalogProtocol;
  onChange: (draft: RequestExampleDraft) => void;
}) {
  const t = useTranslations("apiCatalog");
  const headers = Object.entries(draft.headers);
  const headerRowIDPrefix = useId();
  const [headerRowIDs, setHeaderRowIDs] = useState(() => headers.map(
    (_, index) => `${headerRowIDPrefix}-request-header-${index}`,
  ));
  const [nextHeaderRowID, setNextHeaderRowID] = useState(headers.length);
  const update = <K extends keyof RequestExampleDraft>(key: K, value: RequestExampleDraft[K]) =>
    onChange({ ...draft, [key]: value });
  const addHeader = () => {
    let name = "X-Header";
    let suffix = 2;
    const names = new Set(Object.keys(draft.headers).map((current) => current.toLowerCase()));
    while (names.has(name.toLowerCase())) name = `X-Header-${suffix++}`;
    setHeaderRowIDs((current) => [...current, `${headerRowIDPrefix}-request-header-${nextHeaderRowID}`]);
    setNextHeaderRowID((current) => current + 1);
    update("headers", { ...draft.headers, [name]: "" });
  };
  const updateHeader = (oldName: string, nextName: string, value: string) => {
    const nextHeaders = Object.fromEntries(Object.entries(draft.headers).flatMap(([name, currentValue]) => {
      if (name !== oldName) return [[name, currentValue]];
      return [[nextName, value]];
    }));
    update("headers", nextHeaders);
  };
  const removeHeader = (name: string, index: number) => {
    setHeaderRowIDs((current) => current.filter((_, currentIndex) => currentIndex !== index));
    update("headers", Object.fromEntries(headers.filter(([current]) => current !== name)));
  };

  return (
    <FieldGroup className="gap-4">
      <div className="grid min-w-0 gap-4 md:grid-cols-[8rem_minmax(0,1fr)]">
        <Field>
          <FieldLabel htmlFor="invocation-method">{t("method")}</FieldLabel>
          <Input id="invocation-method" value={draft.method} onChange={(event) => update("method", event.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="invocation-subpath">{t("subpath")}</FieldLabel>
          <Input id="invocation-subpath" value={draft.subpath} onChange={(event) => update("subpath", event.target.value)} />
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor="invocation-query">{t("query")}</FieldLabel>
        <Input id="invocation-query" value={draft.query} onChange={(event) => update("query", event.target.value)} />
      </Field>
      <Field>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <FieldLabel>{t("exampleRequest")}</FieldLabel>
          <Button type="button" variant="outline" size="sm" onClick={addHeader}>
            <Plus data-icon="inline-start" />
            {t("addHeader")}
          </Button>
        </div>
        {headers.length ? (
          <FieldGroup className="gap-3">
            {headers.map(([name, value], index) => (
              <HeaderRow
                key={headerRowIDs[index]}
                rowID={headerRowIDs[index]}
                index={index}
                name={name}
                value={value}
                otherNames={headers.flatMap(([otherName]) => otherName === name ? [] : [otherName])}
                onChange={(nextName, nextValue) => updateHeader(name, nextName, nextValue)}
                onRemove={() => removeHeader(name, index)}
              />
            ))}
          </FieldGroup>
        ) : null}
      </Field>
      {protocol === "http" ? <BodyEditor body={draft.body} onChange={(body) => update("body", body)} /> : null}
    </FieldGroup>
  );
}

function BodyEditor({ body, onChange }: { body: string; onChange: (body: string) => void }) {
  const t = useTranslations("apiCatalog");
  const [editing, setEditing] = useState(false);
  if (!body && !editing) return null;
  return (
    <Field>
      <FieldLabel htmlFor="invocation-body">{t("body")}</FieldLabel>
      <Textarea
        id="invocation-body"
        rows={5}
        value={body}
        onFocus={() => setEditing(true)}
        onBlur={() => setEditing(false)}
        onChange={(event) => onChange(event.target.value)}
      />
    </Field>
  );
}

function HeaderRow({
  rowID,
  index,
  name,
  value,
  otherNames,
  onChange,
  onRemove,
}: {
  rowID: string;
  index: number;
  name: string;
  value: string;
  otherNames: string[];
  onChange: (name: string, value: string) => void;
  onRemove: () => void;
}) {
  const t = useTranslations("apiCatalog");
  const [candidateName, setCandidateName] = useState(name);
  const errorID = `${rowID}-error`;
  const normalizedCandidate = candidateName.trim().toLowerCase();
  const authorization = isGatewayAuthorizationHeader(candidateName);
  const duplicate = otherNames.some((otherName) => otherName.trim().toLowerCase() === normalizedCandidate);
  const invalid = authorization || normalizedCandidate === "" || duplicate;
  const error = authorization ? t("unsafeExampleHeader") : t("invalidExampleHeader");

  return (
    <Field data-invalid={invalid}>
      <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
        <Input
          aria-label={`${t("headerName")} ${index + 1}`}
          aria-invalid={invalid}
          aria-describedby={invalid ? errorID : undefined}
          value={candidateName}
          onChange={(event) => {
            const nextName = event.target.value;
            setCandidateName(nextName);
            const normalized = nextName.trim().toLowerCase();
            const nextInvalid = isGatewayAuthorizationHeader(nextName)
              || normalized === ""
              || otherNames.some((otherName) => otherName.trim().toLowerCase() === normalized);
            if (!nextInvalid) onChange(nextName, value);
          }}
        />
        <Input
          aria-label={`${t("headerValue")} ${index + 1}`}
          value={value}
          onChange={(event) => onChange(name, event.target.value)}
        />
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t("removeHeader")} onClick={onRemove}>
          <Trash2 />
        </Button>
      </div>
      {invalid ? <FieldError id={errorID}>{error}</FieldError> : null}
    </Field>
  );
}

function CommandPreview({ command }: { command: string }) {
  const t = useTranslations("apiCatalog");
  return (
    <Field>
      <FieldLabel>{t("copyCommand")}</FieldLabel>
      <pre
        data-testid="invocation-command-preview"
        className="min-w-0 max-w-full overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted/50 p-3 font-mono text-xs"
      >
        <code>{command}</code>
      </pre>
    </Field>
  );
}

function InvocationActions({
  canCopyReal,
  onChooseToken,
  onCopyTemplate,
  onCopyWithToken,
}: {
  canCopyReal: boolean;
  onChooseToken: () => void;
  onCopyTemplate: () => void;
  onCopyWithToken: () => void;
}) {
  const t = useTranslations("apiCatalog");

  return (
    <div data-testid="invocation-actions" className="flex flex-wrap items-center gap-2">
      <Button type="button" size="sm" disabled={!canCopyReal} onClick={onCopyWithToken}>
        <KeyRound data-icon="inline-start" />
        {t("copyCommand")}
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={onCopyTemplate}>
        <Copy data-icon="inline-start" />
        {t("copyTemplate")}
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={onChooseToken}>
        <KeyRound data-icon="inline-start" />
        {t("changeToken")}
      </Button>
    </div>
  );
}
