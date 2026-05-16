"use client";

import { useTranslations } from "next-intl";
import { RoleMappingEditor } from "@/components/channel/role-mapping-editor";
import { AgentRouteEditor } from "@/components/agent-route-editor";
import { ChannelForm } from "../types";

export interface RouteRolesSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  channelId?: number;
}

export function RouteRolesSection({ form, setForm, channelId }: RouteRolesSectionProps) {
  const t = useTranslations("channels");

  return (
    <div className="space-y-4">
      {/* Role Mapping */}
      <RoleMappingEditor
        value={form.role_mapping}
        onChange={(v) => setForm({ ...form, role_mapping: v })}
      />

      {/* Agent Route Editor (edit mode only) */}
      {channelId !== undefined ? (
        <AgentRouteEditor sourceType="channel" sourceId={channelId} />
      ) : (
        <p className="text-sm text-muted-foreground">{t("agentRouteCreateHint")}</p>
      )}
    </div>
  );
}
