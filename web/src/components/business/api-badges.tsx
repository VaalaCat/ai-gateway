"use client";

import { useTranslations } from "next-intl";
import type { VariantProps } from "class-variance-authority";

import { Badge, badgeVariants } from "@/components/ui/badge";
import type { APIProtocol } from "@/lib/api/api-services";

type BadgeVariant = NonNullable<VariantProps<typeof badgeVariants>["variant"]>;

interface BadgePresentation<State extends string> {
  state: State;
  variant: BadgeVariant;
  className?: string;
}

type LocalizedBadgePresentation<State extends string, Prefix extends string> = BadgePresentation<State> & {
  label: `${Prefix}.${State}`;
};

type ProtocolState = APIProtocol | "unknown";
type ProtocolPresentation<State extends ProtocolState> = LocalizedBadgePresentation<State, "protocol">;
type ProtocolRegistry = { [State in APIProtocol]: ProtocolPresentation<State> };

const PROTOCOL_STATE: ProtocolRegistry = {
  http: { state: "http", label: "protocol.http", variant: "secondary" },
  websocket: { state: "websocket", label: "protocol.websocket", variant: "outline", className: "bg-info text-info-foreground" },
};

const UNKNOWN_PROTOCOL: ProtocolPresentation<"unknown"> = {
  state: "unknown",
  label: "protocol.unknown",
  variant: "outline",
};

type PermissionScopeState = "global" | "scoped";
type PermissionScopePresentation<State extends PermissionScopeState> = LocalizedBadgePresentation<State, "permission">;
type PermissionScopeRegistry = { [State in PermissionScopeState]: PermissionScopePresentation<State> };

const PERMISSION_SCOPE_STATE: PermissionScopeRegistry = {
  global: { state: "global", label: "permission.global", variant: "outline", className: "bg-info text-info-foreground" },
  scoped: { state: "scoped", label: "permission.scoped", variant: "outline" },
};

type TraceCaptureState = "captured" | "empty" | "truncated" | "skipped" | "unavailable";
type TraceCapturePresentation<State extends TraceCaptureState> = LocalizedBadgePresentation<State, "trace">;
type TraceCaptureRegistry = { [State in TraceCaptureState]: TraceCapturePresentation<State> };

const TRACE_STATE: TraceCaptureRegistry = {
  captured: { state: "captured", label: "trace.captured", variant: "outline", className: "bg-success text-success-foreground" },
  empty: { state: "empty", label: "trace.empty", variant: "secondary" },
  truncated: { state: "truncated", label: "trace.truncated", variant: "outline", className: "bg-warning text-warning-foreground" },
  skipped: { state: "skipped", label: "trace.skipped", variant: "outline", className: "bg-info text-info-foreground" },
  unavailable: { state: "unavailable", label: "trace.unavailable", variant: "destructive" },
};

function hasRegistryKey<Registry extends object>(registry: Registry, key: PropertyKey): key is keyof Registry {
  return key in registry;
}

interface ProtocolBadgeProps {
  protocol: string;
}

export function ProtocolBadge({ protocol }: ProtocolBadgeProps) {
  const t = useTranslations("apiBadges");
  const presentation = hasRegistryKey(PROTOCOL_STATE, protocol) ? PROTOCOL_STATE[protocol] : UNKNOWN_PROTOCOL;

  return (
    <Badge
      data-slot="api-protocol-badge"
      data-state={presentation.state}
      variant={presentation.variant}
      className={presentation.className}
    >
      {t(presentation.label)}
    </Badge>
  );
}

interface HTTPStatusBadgeProps {
  statusCode: number;
}

export function HTTPStatusBadge({ statusCode }: HTTPStatusBadgeProps) {
  const t = useTranslations("apiLogs");
  const success = statusCode > 0 && statusCode < 400;
  const label = statusCode === 0
    ? t("noResponse")
    : `${t(success ? "statusSuccess" : "statusFailed")} ${statusCode}`;

  return (
    <Badge
      data-slot="api-http-status-badge"
      data-state={success ? "success" : "failed"}
      variant={success ? "default" : "destructive"}
      className={success ? "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200" : undefined}
    >
      {label}
    </Badge>
  );
}

interface PermissionScopeBadgeProps {
  resourceId: number;
}

export function PermissionScopeBadge({ resourceId }: PermissionScopeBadgeProps) {
  const t = useTranslations("apiBadges");
  const presentation = PERMISSION_SCOPE_STATE[resourceId === 0 ? "global" : "scoped"];

  return (
    <Badge
      data-slot="api-permission-scope-badge"
      data-state={presentation.state}
      variant={presentation.variant}
      className={presentation.className}
    >
      {t(presentation.label)}
    </Badge>
  );
}

interface TraceCaptureBadgeProps {
  status: string;
  reason?: string;
  truncated?: boolean;
}

export function TraceCaptureBadge({ status, truncated = false }: TraceCaptureBadgeProps) {
  const t = useTranslations("apiBadges");
  const state = truncated ? "truncated" : status;
  const presentation = hasRegistryKey(TRACE_STATE, state) ? TRACE_STATE[state] : TRACE_STATE.unavailable;

  return (
    <Badge
      data-slot="api-trace-capture-badge"
      data-state={presentation.state}
      variant={presentation.variant}
      className={presentation.className}
    >
      {t(presentation.label)}
    </Badge>
  );
}
