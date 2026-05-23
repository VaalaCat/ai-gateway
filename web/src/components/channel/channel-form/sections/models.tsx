"use client";

import { useTranslations } from "next-intl";
import { ChevronDown } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ModelMappingInput } from "@/components/ui/model-mapping-input";
import { ModelSelectorPanel } from "@/components/business/model-selector-panel";
import { FetchModelsButton } from "@/components/business/fetch-models-button";
import { CatalogPickerDialog } from "@/components/business/catalog-picker-dialog";
import { FieldTip } from "@/components/business/field-tip";
import { ChannelForm } from "../types";
import { parseSetting } from "../utils";

export interface ModelsSectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
  useModelsCatalog?: () => { data: string[] | undefined };
}

export function ModelsSection({
  form,
  setForm,
  agentId,
  useModelsCatalog,
}: ModelsSectionProps) {
  if (useModelsCatalog) {
    return (
      <CatalogModelsBlock
        form={form}
        setForm={setForm}
        useModelsCatalog={useModelsCatalog}
      />
    );
  }
  return <AdminFetchModelsBlock form={form} setForm={setForm} agentId={agentId} />;
}

function splitModels(models: string): string[] {
  return models ? models.split(",").map((s) => s.trim()).filter(Boolean) : [];
}

interface CatalogModelsBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  useModelsCatalog: () => { data: string[] | undefined };
}

function CatalogModelsBlock({ form, setForm, useModelsCatalog }: CatalogModelsBlockProps) {
  const t = useTranslations("channels");
  const { data } = useModelsCatalog();
  const available = data ?? [];
  const selected = splitModels(form.models);

  return (
    <div className="space-y-4">
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

      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-2">
          <Label>{t("weight")}</Label>
          <Input
            type="number"
            min={1}
            value={form.weight}
            onChange={(e) => setForm({ ...form, weight: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label>{t("priority")}</Label>
          <Input
            type="number"
            value={form.priority}
            onChange={(e) => setForm({ ...form, priority: e.target.value })}
          />
        </div>
      </div>
    </div>
  );
}

interface AdminFetchModelsBlockProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  agentId?: string;
}

function AdminFetchModelsBlock({ form, setForm, agentId }: AdminFetchModelsBlockProps) {
  const t = useTranslations("channels");
  const setting = parseSetting(form.setting);

  return (
    <div className="space-y-4">
      {/* Models */}
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
            existingModels={splitModels(form.models)}
            onModelsSelected={(models) => setForm({ ...form, models: models.join(",") })}
          />
        </div>
        <ModelSelectorPanel
          value={splitModels(form.models)}
          onChange={(models) => setForm({ ...form, models: models.join(",") })}
        />
      </div>

      {/* Model Mapping */}
      <div className="space-y-2">
        <Label>{t("modelMapping")}</Label>
        <ModelMappingInput
          value={form.model_mapping}
          onChange={(json) => setForm({ ...form, model_mapping: json })}
          onMappingAdd={(sourceModel) => {
            const modelList = splitModels(form.models);
            if (!modelList.includes(sourceModel)) {
              setForm({ ...form, models: [...modelList, sourceModel].join(",") });
            }
          }}
          onMappingRemove={(sourceModel) => {
            const modelList = form.models
              .split(",")
              .map((s) => s.trim())
              .filter((m) => m && m !== sourceModel);
            setForm({ ...form, models: modelList.join(",") });
          }}
        />
      </div>

      {/* Test Model */}
      <div className="space-y-2">
        <Label>
          {t("testModel")}
          <FieldTip text={t("testModelTip")} />
        </Label>
        <Input
          value={form.test_model}
          onChange={(e) => setForm({ ...form, test_model: e.target.value })}
        />
      </div>

      {/* Load Balancing */}
      <Collapsible defaultOpen>
        <CollapsibleTrigger asChild>
          <button
            type="button"
            className="flex w-full items-center justify-between rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent"
          >
            {t("sectionLoadBalancing")}
            <ChevronDown className="size-4" />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-4 pt-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{t("weight")}</Label>
              <Input
                type="number"
                value={form.weight}
                onChange={(e) => setForm({ ...form, weight: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("priority")}</Label>
              <Input
                type="number"
                value={form.priority}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
              />
            </div>
          </div>
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}
