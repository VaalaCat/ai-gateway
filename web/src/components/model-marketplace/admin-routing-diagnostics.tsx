"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export interface AdminRoutingMemberDiagnosticsView {
  type: "routing" | "model" | "invalid";
  name: string;
  modelName?: string;
  routingId?: number;
  priority: number;
  weight: number;
}

export interface AdminRoutingPathDiagnosticsView {
  ref: string;
  routingId: number;
}

export interface AdminRoutingDefinitionDiagnosticsView {
  occurrenceId: string;
  path: AdminRoutingPathDiagnosticsView[];
  routingId: number;
  name: string;
  scope: string;
  userId: number;
  tokenId: number;
  enabled: boolean;
  members: AdminRoutingMemberDiagnosticsView[];
}

function formatRoutingPath(path: AdminRoutingDefinitionDiagnosticsView["path"]) {
  return path.map((step) => `${step.ref} (${step.routingId})`).join(" → ");
}

function RoutingMemberDiagnostics({ member }: {
  member: AdminRoutingMemberDiagnosticsView;
}) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <div role="group" aria-label={member.name} className="rounded-md border p-3 text-xs">
      <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
        <span>{t(`adminRoutingMemberType.${member.type}`)}</span>
        <span>{t("adminRoutingMemberName")}: {member.name}</span>
        {member.modelName ? (
          <span>{t("adminRoutingMemberModelName")}: {member.modelName}</span>
        ) : null}
        {member.routingId !== undefined ? (
          <span className="tabular-nums">
            {t("adminRoutingMemberRoutingId")}: {member.routingId}
          </span>
        ) : null}
        <span className="tabular-nums">{t("adminRoutingMemberPriority")}: {member.priority}</span>
        <span className="tabular-nums">{t("adminRoutingMemberWeight")}: {member.weight}</span>
      </div>
    </div>
  );
}

function RoutingDefinitionDiagnostics({ definition }: {
  definition: AdminRoutingDefinitionDiagnosticsView;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const pathDescriptionID = `routing-path-${definition.occurrenceId}`;
  return (
    <Card
      role="article"
      aria-label={definition.name}
      aria-describedby={pathDescriptionID}
      className="gap-3 py-4"
    >
      <CardHeader className="px-4">
        <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
          <span>{definition.name}</span>
          <Badge variant={definition.enabled ? "default" : "secondary"}>
            {t("adminRoutingEnabled")}: {t(`adminBoolean.${definition.enabled}`)}
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 px-4">
        <div className="grid grid-cols-1 gap-1 text-xs sm:grid-cols-2">
          <span className="tabular-nums">{t("adminRoutingId")}: {definition.routingId}</span>
          <span>{t("adminRoutingScope")}: {definition.scope}</span>
          <span className="tabular-nums">{t("adminRoutingUserId")}: {definition.userId}</span>
          <span className="tabular-nums">{t("adminRoutingTokenId")}: {definition.tokenId}</span>
          <span id={pathDescriptionID} className="sm:col-span-2">
            {t("adminRoutingPath")}: {formatRoutingPath(definition.path)}
          </span>
        </div>
        <div className="flex flex-col gap-2">
          {definition.members.map((member, index) => (
            <RoutingMemberDiagnostics key={`${member.name}:${index}`} member={member} />
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export function AdminRoutingDiagnostics({ definitions }: {
  definitions: AdminRoutingDefinitionDiagnosticsView[];
}) {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <section
      role="region"
      aria-labelledby="admin-routing-diagnostics-title"
      className="flex flex-col gap-3"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="admin-routing-diagnostics-title" className="text-lg font-semibold tracking-tight">
          {t("adminRoutingDiagnosticsTitle")}
        </h2>
        <Badge variant="outline">{t("adminDiagnosticsAdminOnly")}</Badge>
      </div>
      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        {definitions.map((definition) => (
          <RoutingDefinitionDiagnostics key={definition.occurrenceId} definition={definition} />
        ))}
      </div>
    </section>
  );
}
