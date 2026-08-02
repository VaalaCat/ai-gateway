"use client";

import { useRef, useState, type FormEvent } from "react";
import { LoaderCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import {
  buildAgentRoutePayload,
  createAgentRouteFormValues,
  type AgentRouteFormValues,
  type AgentRouteSourceType,
  type AgentRouteTargetType,
} from "@/components/agent-route-form-values";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  useCreateAgentRoute,
  useUpdateAgentRoute,
} from "@/lib/api/agent-routes";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { AgentRouteOverviewItem } from "@/lib/types";

interface AgentRouteFormDialogProps {
  open: boolean;
  route: AgentRouteOverviewItem | null;
  onOpenChange: (open: boolean) => void;
}

type InvalidField = "source_id" | "target_value" | null;

function AgentRouteForm({
  route,
  onSuccess,
}: {
  route: AgentRouteOverviewItem | null;
  onSuccess: () => void;
}) {
  const t = useTranslations("agentRoutes");
  const tc = useTranslations("common");
  const [values, setValues] = useState<AgentRouteFormValues>(() =>
    createAgentRouteFormValues(route),
  );
  const [invalidField, setInvalidField] = useState<InvalidField>(null);
  const submitLocked = useRef(false);
  const createMutation = useCreateAgentRoute();
  const updateMutation = useUpdateAgentRoute();
  const pending = createMutation.isPending || updateMutation.isPending;

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitLocked.current) return;

    const result = buildAgentRoutePayload(values);
    if (!result.ok) {
      setInvalidField(result.field);
      toast.error(t(result.field === "source_id" ? "sourceRequired" : "targetValueRequired"));
      return;
    }

    submitLocked.current = true;
    try {
      if (route) {
        await updateMutation.mutateAsync({ id: route.id, ...result.payload });
        toast.success(t("ruleUpdated"));
      } else {
        await createMutation.mutateAsync(result.payload);
        toast.success(t("ruleAdded"));
      }
      onSuccess();
    } catch (error) {
      toast.error(formatErrorToast(error, route ? t("updateFailed") : t("addFailed")));
    } finally {
      submitLocked.current = false;
    }
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-5">
      <FieldGroup className="gap-5">
        <Field>
          <FieldLabel htmlFor="agent-route-source-type">{t("sourceType")}</FieldLabel>
          <Select
            value={values.sourceType}
            onValueChange={(sourceType) => {
              setValues({
                ...values,
                sourceType: sourceType as AgentRouteSourceType,
                sourceId: "",
              });
              setInvalidField(null);
            }}
          >
            <SelectTrigger id="agent-route-source-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="token">{t("sourceToken")}</SelectItem>
                <SelectItem value="channel">{t("sourceChannel")}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>

        <Field data-invalid={invalidField === "source_id"}>
          <FieldLabel>{t("source")}</FieldLabel>
          <EntityPicker
            key={values.sourceType}
            entity={values.sourceType}
            value={values.sourceId}
            onChange={(sourceId) => {
              setValues({ ...values, sourceId });
              setInvalidField(null);
            }}
            placeholder={values.sourceType === "token" ? t("selectToken") : t("selectChannel")}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor="agent-route-model">{t("model")}</FieldLabel>
          <Input
            id="agent-route-model"
            value={values.model}
            onChange={(event) => setValues({ ...values, model: event.target.value })}
            placeholder={t("modelPlaceholder")}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor="agent-route-target-type">{t("targetType")}</FieldLabel>
          <Select
            value={values.targetType}
            onValueChange={(targetType) => {
              setValues({
                ...values,
                targetType: targetType as AgentRouteTargetType,
                targetValue: "",
              });
              setInvalidField(null);
            }}
          >
            <SelectTrigger id="agent-route-target-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="agent_id">{t("agentId")}</SelectItem>
                <SelectItem value="agent_tag">{t("agentTag")}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>

        <Field data-invalid={invalidField === "target_value"}>
          <FieldLabel htmlFor={values.targetType === "agent_tag" ? "agent-route-tag" : undefined}>
            {t("targetValue")}
          </FieldLabel>
          {values.targetType === "agent_id" ? (
            <EntityPicker
              entity="agent"
              value={values.targetValue}
              onChange={(targetValue) => {
                setValues({ ...values, targetValue });
                setInvalidField(null);
              }}
              placeholder={t("selectAgent")}
            />
          ) : (
            <Input
              id="agent-route-tag"
              value={values.targetValue}
              onChange={(event) => {
                setValues({ ...values, targetValue: event.target.value });
                setInvalidField(null);
              }}
              placeholder={t("agentTagPlaceholder")}
              aria-invalid={invalidField === "target_value"}
            />
          )}
        </Field>
      </FieldGroup>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onSuccess} disabled={pending}>
          {tc("cancel")}
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : null}
          {route ? tc("save") : tc("create")}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function AgentRouteFormDialog({
  open,
  route,
  onOpenChange,
}: AgentRouteFormDialogProps) {
  const t = useTranslations("agentRoutes");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{route ? t("editRule") : t("createRule")}</DialogTitle>
        </DialogHeader>
        {open ? (
          <AgentRouteForm
            key={route ? `edit-${route.id}` : "create"}
            route={route}
            onSuccess={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
