"use client";

import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { useAgentRoutes, useCreateAgentRoute, useDeleteAgentRoute } from "@/lib/api/agent-routes";
import { useAgents } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";

interface AgentRouteEditorProps {
  sourceType: "token" | "channel";
  sourceId: number;
}

interface NewRouteForm {
  model: string;
  targetType: "agent_id" | "agent_tag";
  targetValue: string;
}

const DEFAULT_NEW_FORM: NewRouteForm = {
  model: "",
  targetType: "agent_id",
  targetValue: "",
};

export function AgentRouteEditor({ sourceType, sourceId }: AgentRouteEditorProps) {
  const t = useTranslations("agentRoutes");
  const tc = useTranslations("common");
  const [showAddForm, setShowAddForm] = useState(false);
  const [newForm, setNewForm] = useState<NewRouteForm>(DEFAULT_NEW_FORM);

  const { data: routesData, isLoading: routesLoading } = useAgentRoutes({
    source_type: sourceType,
    source_id: sourceId,
  });

  const { data: agentsData } = useAgents({ page_size: 100 });

  const createMutation = useCreateAgentRoute();
  const deleteMutation = useDeleteAgentRoute();

  const routes = routesData?.data ?? [];
  const agents = agentsData?.data ?? [];

  const handleDelete = async (id: number) => {
    try {
      await deleteMutation.mutateAsync(id);
      toast.success(t("ruleDeleted"));
    } catch (e) {
      toast.error(formatErrorToast(e, t("deleteFailed")));
    }
  };

  const handleAdd = async () => {
    if (!newForm.targetValue.trim()) {
      toast.error(t("targetValueRequired"));
      return;
    }

    try {
      const body: Parameters<typeof createMutation.mutateAsync>[0] = {
        source_type: sourceType,
        source_id: sourceId,
        model: newForm.model.trim() || "",
      };

      if (newForm.targetType === "agent_id") {
        body.agent_id = newForm.targetValue.trim();
      } else {
        body.agent_tag = newForm.targetValue.trim();
      }

      await createMutation.mutateAsync(body);
      toast.success(t("ruleAdded"));
      setNewForm(DEFAULT_NEW_FORM);
      setShowAddForm(false);
    } catch (e) {
      toast.error(formatErrorToast(e, t("addFailed")));
    }
  };

  const handleCancel = () => {
    setNewForm(DEFAULT_NEW_FORM);
    setShowAddForm(false);
  };

  return (
    <div className="space-y-2">
      {/* Section title */}
      <Label className="text-sm font-medium">{t("title")}</Label>

      {/* Existing routes */}
      {routesLoading ? (
        <p className="text-xs text-muted-foreground">{tc("loading")}</p>
      ) : routes.length === 0 && !showAddForm ? (
        <p className="text-xs text-muted-foreground">{t("noRules")}</p>
      ) : (
        <div className="space-y-1">
          {routes.map((route) => (
            <div
              key={route.id}
              className="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm"
            >
              {/* Model */}
              <Badge variant="outline" className="shrink-0 font-mono text-xs">
                {route.model ? route.model : t("default")}
              </Badge>

              <span className="text-muted-foreground shrink-0">→</span>

              {/* Target */}
              <div className="flex min-w-0 flex-1 items-center gap-1">
                <span className="shrink-0 text-xs text-muted-foreground">
                  {route.agent_id ? `${t("agentId")}:` : `${t("agentTag")}:`}
                </span>
                <span className="truncate font-mono text-xs">
                  {route.agent_id || route.agent_tag}
                </span>
              </div>

              {/* Delete button */}
              <Button
                variant="ghost"
                size="icon"
                className="size-6 shrink-0"
                onClick={() => handleDelete(route.id)}
                disabled={deleteMutation.isPending}
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}

      {/* Add form */}
      {showAddForm && (
        <div className="space-y-2 rounded-md border p-3">
          {/* Model input */}
          <div className="flex items-center gap-2">
            <Label className="w-16 shrink-0 text-xs">{t("model")}</Label>
            <Input
              className="h-7 text-xs"
              placeholder={t("modelPlaceholder")}
              value={newForm.model}
              onChange={(e) => setNewForm({ ...newForm, model: e.target.value })}
            />
          </div>

          {/* Target type */}
          <div className="flex items-center gap-2">
            <Label className="w-16 shrink-0 text-xs">{t("targetType")}</Label>
            <Select
              value={newForm.targetType}
              onValueChange={(v) =>
                setNewForm({ ...newForm, targetType: v as "agent_id" | "agent_tag", targetValue: "" })
              }
            >
              <SelectTrigger className="h-7 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="agent_id">{t("agentId")}</SelectItem>
                <SelectItem value="agent_tag">{t("agentTag")}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* Target value */}
          <div className="flex items-center gap-2">
            <Label className="w-16 shrink-0 text-xs">{t("targetValue")}</Label>
            {newForm.targetType === "agent_id" ? (
              <Select
                value={newForm.targetValue}
                onValueChange={(v) => setNewForm({ ...newForm, targetValue: v })}
              >
                <SelectTrigger className="h-7 text-xs">
                  <SelectValue placeholder={t("selectAgent")} />
                </SelectTrigger>
                <SelectContent>
                  {agents.map((agent) => (
                    <SelectItem key={agent.agent_id} value={agent.agent_id}>
                      {agent.name} ({agent.agent_id})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                className="h-7 text-xs"
                placeholder={t("agentTagPlaceholder")}
                value={newForm.targetValue}
                onChange={(e) => setNewForm({ ...newForm, targetValue: e.target.value })}
              />
            )}
          </div>

          {/* Form actions */}
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" className="h-7 text-xs" onClick={handleCancel}>
              {tc("cancel")}
            </Button>
            <Button
              size="sm"
              className="h-7 text-xs"
              onClick={handleAdd}
              disabled={createMutation.isPending}
            >
              {t("add")}
            </Button>
          </div>
        </div>
      )}

      {/* Add button */}
      {!showAddForm && (
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs"
          onClick={() => setShowAddForm(true)}
        >
          <Plus className="mr-1 size-3.5" />
          {t("addRule")}
        </Button>
      )}
    </div>
  );
}
