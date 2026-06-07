"use client";

import { useTranslations } from "next-intl";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useOnlineAgents } from "@/lib/api/agents";
import type { OnlineAgentInfo } from "@/lib/types";

interface OnlineAgentSelectProps {
  /** 空串表示 local 本机测试 */
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}

// 测试节点选择器：只列在线 agent + 一个 local 哨兵项。语义专用，不套通用 EntityPicker。
export function OnlineAgentSelect({ value, onChange, disabled }: OnlineAgentSelectProps) {
  const t = useTranslations("channels");
  const { data: onlineAgents } = useOnlineAgents();
  return (
    <Select
      value={value || "local"}
      onValueChange={(v) => onChange(v === "local" ? "" : v)}
      disabled={disabled}
    >
      <SelectTrigger className="w-[200px]">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="local">{t("localTest")}</SelectItem>
        {onlineAgents?.map((a: OnlineAgentInfo) => (
          <SelectItem key={a.agent_id} value={a.agent_id}>
            {a.name} ({a.agent_id.slice(0, 8)}...)
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
