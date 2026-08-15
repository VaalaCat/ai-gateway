"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronDown, Copy, KeyRound, RefreshCw } from "lucide-react";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { APIProtocol, APIRequestExample } from "@/lib/api/api-services";
import { useAuth } from "@/lib/auth";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import { buildInvocationCommand, buildInvocationCommands, type InvocationResult } from "./invocation-command";
import { useInvocationToken } from "./use-invocation-token";

export interface InvocationCommandRoute {
  id: number;
  slug: string;
  protocols: APIProtocol[];
  allowed_methods?: string[];
  websocket_subprotocols?: string[];
  example_request?: APIRequestExample;
}

interface CopyInvocationCommandProps {
  origin: string;
  serviceId: number;
  serviceSlug: string;
  route: InvocationCommandRoute;
}

type CommandKind = InvocationResult["kind"];

function exampleForRoute(route: InvocationCommandRoute): APIRequestExample {
  if (route.example_request) return route.example_request;
  const websocketProtocol = route.websocket_subprotocols?.[0];
  return {
    method: route.allowed_methods?.[0] || "GET",
    subpath: "",
    query: "",
    headers: websocketProtocol ? { "Sec-WebSocket-Protocol": websocketProtocol } : {},
    body: "",
  };
}

function defaultCommandKind(protocols: APIProtocol[]): CommandKind {
  return protocols.includes("http") ? "curl" : "websocat";
}

export function CopyInvocationCommand({ origin, serviceId, serviceSlug, route }: CopyInvocationCommandProps) {
  const t = useTranslations("apiServices");
  const { user } = useAuth();
  const viewerUserID = user?.user_id ?? 0;
  const scopeKey = `${viewerUserID}:${serviceId}:${route.id}`;
  const invocationToken = useInvocationToken({
    viewerUserID,
    apiServiceID: serviceId,
    apiRouteID: route.id,
  }, { rememberScope: "route" });
  const [candidate, setCandidate] = useState({ scopeKey: "", selected: false });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [preferredCommandKind, setPreferredCommandKind] = useState<CommandKind>(() => defaultCommandKind(route.protocols));
  const commandKind = route.protocols.includes(preferredCommandKind === "curl" ? "http" : "websocket")
    ? preferredCommandKind
    : defaultCommandKind(route.protocols);
  const tokenID = invocationToken.tokenID;
  const selectedToken = invocationToken.token;
  const unrememberedSelection = candidate.scopeKey === scopeKey && candidate.selected;
  const validatingRememberedToken = !unrememberedSelection && invocationToken.isChecking;
  const example = exampleForRoute(route);
  const templateInput = { origin, serviceSlug, routeSlug: route.slug, protocols: route.protocols, example };
  const templateCommands = origin ? buildInvocationCommands({ ...templateInput, token: "${API_TOKEN}" }) : [];
  const supportsMultipleProtocols = route.protocols.includes("http") && route.protocols.includes("websocket");
  const selectedTemplate = templateCommands.find((command) => command.kind === commandKind)
    ?? templateCommands[0]
    ?? (origin ? buildInvocationCommand({ ...templateInput, token: "${API_TOKEN}" }) : { kind: "curl" as const, publicUrl: "", command: "" });
  const currentValidationFailure = invocationToken.failure;
  const rememberedSelectedToken = !unrememberedSelection ? selectedToken : undefined;

  const commandFor = (tokenValue: string) => buildInvocationCommands({ ...templateInput, token: tokenValue })
    .find((command) => command.kind === commandKind)
    ?? buildInvocationCommand({ ...templateInput, token: tokenValue });

  const copyTemplate = async () => copyTextWithFeedback(selectedTemplate.command, {
    success: t("templateCommandCopied"),
    error: t("copyCommandFailed"),
  });

  const copyWithToken = async (remember: boolean) => {
    if (!selectedToken?.key) return false;
    const copied = await copyTextWithFeedback(commandFor(selectedToken.key).command, {
      success: t("commandCopiedWithToken", { name: selectedToken.name }),
      error: t("copyCommandFailed"),
    });
    if (copied && remember) {
      invocationToken.rememberToken();
      setCandidate({ scopeKey: "", selected: false });
    }
    if (copied) setDialogOpen(false);
    return copied;
  };

  const selectToken = (value: string) => {
    invocationToken.setTokenID(Number(value) || 0);
    setCandidate({ scopeKey, selected: true });
  };

  const openCommandDialog = () => setDialogOpen(true);

  return (
    <div className="flex flex-wrap items-center gap-1">
      <Button
        type="button"
        size="sm"
        disabled={!origin}
        onClick={() => rememberedSelectedToken?.key ? void copyWithToken(false) : openCommandDialog()}
      >
        <Copy data-icon="inline-start" />{t("copyCommand")}
      </Button>
      {rememberedSelectedToken?.key ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="outline" size="icon-sm" aria-label={t("copyCommandOptions")}><ChevronDown /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={openCommandDialog}><RefreshCw />{t("changeInvocationToken")}</DropdownMenuItem>
              <DropdownMenuItem onSelect={() => void copyTemplate()}><Copy />{t("copyTemplateCommand")}</DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chooseInvocationToken")}</DialogTitle>
            <DialogDescription>{t("chooseInvocationTokenDescription")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor={`route-token-${route.id}`}>{t("token")}</FieldLabel>
              <EntityPicker
                id={`route-token-${route.id}`}
                entity="usable-token"
                apiServiceId={serviceId}
                apiRouteId={route.id}
                value={tokenID ? String(tokenID) : ""}
                onChange={selectToken}
                placeholder={t("chooseInvocationToken")}
              />
            </Field>
            {supportsMultipleProtocols ? (
              <Field>
                <FieldLabel>{t("invocationProtocol")}</FieldLabel>
                <ToggleGroup type="single" variant="outline" size="sm" aria-label={t("invocationProtocol")} value={commandKind} onValueChange={(value) => { if (value) setPreferredCommandKind(value as CommandKind); }}>
                  <ToggleGroupItem value="curl" aria-label={t("httpProtocol")}>{t("httpProtocol")}</ToggleGroupItem>
                  <ToggleGroupItem value="websocat" aria-label={t("websocketProtocol")}>{t("websocketProtocol")}</ToggleGroupItem>
                </ToggleGroup>
              </Field>
            ) : null}
          </FieldGroup>
          {currentValidationFailure ? (
            <Alert variant="destructive">
              <AlertTitle>{t(currentValidationFailure === "error" ? "invocationTokenValidationFailed" : "invocationTokenNoLongerAllowed")}</AlertTitle>
              <AlertDescription>{t("chooseInvocationTokenDescription")}</AlertDescription>
            </Alert>
          ) : null}
          <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs"><code>{selectedTemplate.command}</code></pre>
          <DialogFooter>
            <DialogClose asChild><Button type="button" variant="outline" size="sm">{t("cancel")}</Button></DialogClose>
            <Button type="button" variant="outline" size="sm" disabled={!origin} onClick={() => void copyTemplate()}><Copy data-icon="inline-start" />{t("copyTemplateCommand")}</Button>
            <Button type="button" size="sm" disabled={!selectedToken?.key || invocationToken.isChecking} onClick={() => void copyWithToken(true)}><KeyRound data-icon="inline-start" />{t("copyAndRememberToken")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {!dialogOpen && validatingRememberedToken ? <span className="text-xs text-muted-foreground">{t("invocationTokenChecking")}</span> : null}
      {!dialogOpen && currentValidationFailure ? <span className="text-xs text-destructive">{t(currentValidationFailure === "error" ? "invocationTokenValidationFailed" : "invocationTokenNoLongerAllowed")}</span> : null}
    </div>
  );
}
