"use client";

import { useTranslations } from "next-intl";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { StatusSelect } from "@/components/business/status-select";
import { ModelSelectorPanel } from "@/components/business/model-selector-panel";
import { FetchModelsButton } from "@/components/business/fetch-models-button";
import { CatalogPickerDialog } from "@/components/business/catalog-picker-dialog";
import { FieldTip } from "@/components/business/field-tip";
import { AgentRouteEditor } from "@/components/agent-route-editor";
import { ChannelForm } from "../types";
import { parseSetting, parseLimit, stringifyLimit } from "../utils";
import { LimitRulesEditor } from "../limit-rules-editor";

export interface RoutingSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
  agentTag?: string;
  useModelsCatalog?: () => { data: string[] | undefined };
  hiddenFields?: ReadonlySet<keyof ChannelForm>;
  showStatus?: boolean;
  channelId?: number;
}

function splitModels(models: string): string[] {
  return models ? models.split(",").map((s) => s.trim()).filter(Boolean) : [];
}

export function RoutingSection({
  form,
  setForm,
  agentId,
  agentTag,
  useModelsCatalog,
  hiddenFields,
  showStatus,
  channelId,
}: RoutingSectionProps) {
  const t = useTranslations("channels");
  const limit = parseLimit(form.limit);
  const modelCount = splitModels(form.models).length;
  const ruleCount = (limit.rules ?? []).length;
  const showLimit = !hiddenFields?.has("limit");

  return (
    <Accordion type="multiple" defaultValue={["status", "models", "weight"]} className="rounded-md border">
      <AccordionItem value="status">
        <AccordionTrigger className="px-3">{t("routingGroupStatus")}</AccordionTrigger>
        <AccordionContent className="space-y-4 px-3 pb-3">
          {showStatus && (
            <StatusSelect value={form.status} onChange={(v) => setForm({ ...form, status: v })} />
          )}
          <div className="flex items-center justify-between">
            <Label>
              {t("autoBan")}
              <FieldTip text={t("autoBanTip")} />
            </Label>
            <Switch
              checked={form.auto_ban === "1"}
              onCheckedChange={(v) => setForm({ ...form, auto_ban: v ? "1" : "0" })}
            />
          </div>
        </AccordionContent>
      </AccordionItem>

      <AccordionItem value="models">
        <AccordionTrigger className="px-3">
          <div className="flex flex-1 items-center justify-between pr-2">
            <span>{t("models")}</span>
            <span className="text-xs font-normal text-muted-foreground">{modelCount || ""}</span>
          </div>
        </AccordionTrigger>
        <AccordionContent className="space-y-3 px-3 pb-3">
          {useModelsCatalog ? (
            <CatalogModelsListBlock form={form} setForm={setForm} useModelsCatalog={useModelsCatalog} />
          ) : (
            <AdminModelsListBlock form={form} setForm={setForm} agentId={agentId} agentTag={agentTag} />
          )}
        </AccordionContent>
      </AccordionItem>

      <AccordionItem value="weight">
        <AccordionTrigger className="px-3">{t("routingGroupWeight")}</AccordionTrigger>
        <AccordionContent className="grid grid-cols-1 gap-4 px-3 pb-3 sm:grid-cols-2">
          <div className="space-y-2">
            <Label>{t("weight")}</Label>
            <Input type="number" min={1} value={form.weight} onChange={(e) => setForm({ ...form, weight: e.target.value })} />
          </div>
          <div className="space-y-2">
            <Label>{t("priority")}</Label>
            <Input type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })} />
          </div>
        </AccordionContent>
      </AccordionItem>

      <AccordionItem value="test">
        <AccordionTrigger className="px-3">{t("routingGroupTest")}</AccordionTrigger>
        <AccordionContent className="space-y-2 px-3 pb-3">
          <Label>
            {t("testModel")}
            <FieldTip text={t("testModelTip")} />
          </Label>
          <Input value={form.test_model} onChange={(e) => setForm({ ...form, test_model: e.target.value })} />
        </AccordionContent>
      </AccordionItem>

      {showLimit && (
        <AccordionItem value="limit">
          <AccordionTrigger className="px-3">
            <div className="flex flex-1 items-center justify-between pr-2">
              <span>{t("usageLimit")}</span>
              <span className="text-xs font-normal text-muted-foreground">{ruleCount || ""}</span>
            </div>
          </AccordionTrigger>
          <AccordionContent className="space-y-4 px-3 pb-3">
            <LimitRulesEditor limit={limit} onChange={(next) => setForm({ ...form, limit: stringifyLimit(next) })} />
          </AccordionContent>
        </AccordionItem>
      )}

      <AccordionItem value="agent-route">
        <AccordionTrigger className="px-3">{t("routingGroupAgentRoute")}</AccordionTrigger>
        <AccordionContent className="px-3 pb-3">
          {channelId !== undefined ? (
            <AgentRouteEditor sourceType="channel" sourceId={channelId} />
          ) : (
            <p className="text-sm text-muted-foreground">{t("agentRouteCreateHint")}</p>
          )}
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}

/* ── Catalog variant models block ─────────────────────────────────────── */

interface CatalogModelsListBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  useModelsCatalog: () => { data: string[] | undefined };
}

function CatalogModelsListBlock({ form, setForm, useModelsCatalog }: CatalogModelsListBlockProps) {
  const t = useTranslations("channels");
  const { data } = useModelsCatalog();
  const available = data ?? [];
  const selected = splitModels(form.models);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{t("models")}</Label>
        <CatalogPickerDialog
          available={available}
          alreadySelected={selected}
          disabled={available.length === 0}
          onConfirm={(added) =>
            setForm({
              ...form,
              models: [...selected, ...added].join(","),
            })
          }
        />
      </div>
      <ModelSelectorPanel
        value={selected}
        onChange={(models) => setForm({ ...form, models: models.join(",") })}
      />
    </div>
  );
}

/* ── Admin fetch variant models block ─────────────────────────────────── */

interface AdminModelsListBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
  agentTag?: string;
}

function AdminModelsListBlock({ form, setForm, agentId, agentTag }: AdminModelsListBlockProps) {
  const t = useTranslations("channels");
  const setting = parseSetting(form.setting);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{t("models")}</Label>
        <FetchModelsButton
          baseUrl={form.base_url}
          apiKey={form.key}
          channelType={Number(form.type)}
          endpoints={form.endpoints}
          proxyUrl={form.proxy_url || setting.proxy}
          agentId={agentId}
          agentTag={agentTag}
          existingModels={splitModels(form.models)}
          onModelsSelected={(models) => setForm({ ...form, models: models.join(",") })}
        />
      </div>
      <ModelSelectorPanel
        value={splitModels(form.models)}
        onChange={(models) => setForm({ ...form, models: models.join(",") })}
      />
    </div>
  );
}
