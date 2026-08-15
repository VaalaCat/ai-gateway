"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { StatusSelect } from "@/components/business/status-select";
import { NumberUnitInput } from "@/components/business/number-unit-input";
import { ModelMappingInput } from "@/components/ui/model-mapping-input";
import { RoleMappingEditor } from "@/components/channel/role-mapping-editor";
import { LimitRulesEditor } from "./channel-form/limit-rules-editor";
import {
  batchFieldGroupMessageKeys,
  type BatchFieldGroupMessageKey,
  type BatchFieldMessageKey,
} from "./batch-edit-runtime-message-keys";
import { AffinitySection } from "./channel-form/sections/affinity";
import { ResilienceSection } from "./channel-form/sections/resilience";
import { ChannelEndpointsEditor } from "./channel-form/channel-endpoints-editor";
import { emptyForm, type ChannelForm } from "./channel-form/types";
import {
  parseAffinity,
  parseLimit,
  parseResilience,
  getPublicDisplayNameValidationError,
  serializeChannelLimitForPayload,
  stringifyLimit,
} from "./channel-form/utils";
import { useBatchEditChannels } from "@/lib/api/channels";
import { cn } from "@/lib/utils";
import { humanizeNumberUnit } from "@/lib/utils/number-unit";
import type {
  BatchEditableChannelFields,
  BatchEditChannelRequest,
  BatchEditChannelResponse,
  NonEmptyPartial,
} from "@/lib/types";

type BatchEditableKey = keyof BatchEditableChannelFields;
type BatchFormValue<K extends BatchEditableKey> = ChannelForm[K];
type BatchFieldState<T> = { enabled: boolean; value: T };
type BatchEditState = { [K in BatchEditableKey]: BatchFieldState<BatchFormValue<K>> };
type BatchFieldGroup = BatchFieldGroupMessageKey;
type BatchFieldControl<K extends BatchEditableKey> =
  K extends "status" ? "status"
  : K extends "model_mapping" ? "modelMapping"
  : K extends "role_mapping" ? "roleMapping"
  : K extends "limit" ? "limit"
  : K extends "affinity" ? "affinity"
  : K extends "resilience" ? "resilience"
  : K extends "endpoints" ? "endpoints"
  : BatchEditableChannelFields[K] extends boolean ? "switch"
  : BatchEditableChannelFields[K] extends number ? "number"
  : K extends "remark" | "system_prompt" | "param_override" | "header_override" | "setting" | "other_settings" | "status_code_mapping" ? "textarea"
  : "input";

interface BatchFieldDefinition<K extends BatchEditableKey> {
  key: K;
  label: BatchFieldMessageKey;
  group: BatchFieldGroup;
  control: BatchFieldControl<K>;
  initial: BatchFormValue<K>;
  serialize(value: BatchFormValue<K>): BatchEditableChannelFields[K];
}

function batchField<K extends BatchEditableKey>(
  key: K,
  label: BatchFieldMessageKey,
  group: BatchFieldGroup,
  control: BatchFieldControl<K>,
  serialize: (value: BatchFormValue<K>) => BatchEditableChannelFields[K],
  initial: BatchFormValue<K> = emptyForm[key],
): BatchFieldDefinition<K> {
  return { key, label, group, control, serialize, initial };
}

function serializePublicDisplayName(value: string): string {
  return value.trim();
}

const batchFieldRegistry = [
  batchField("public_display_name", "publicDisplayName", "batchEditGroupBasic", "input", serializePublicDisplayName),
  batchField("type", "type", "batchEditGroupBasic", "number", Number),
  batchField("key", "apiKey", "batchEditGroupBasic", "input", String),
  batchField("base_url", "baseUrl", "batchEditGroupBasic", "input", String),
  batchField("tag", "tag", "batchEditGroupBasic", "input", String),
  batchField("remark", "remark", "batchEditGroupBasic", "textarea", String),
  batchField("status", "batchEditFieldStatus", "batchEditGroupRouting", "status", Number),
  batchField("models", "models", "batchEditGroupRouting", "input", String),
  batchField("weight", "weight", "batchEditGroupRouting", "number", Number),
  batchField("priority", "priority", "batchEditGroupRouting", "number", Number),
  batchField("test_model", "testModel", "batchEditGroupRouting", "input", String),
  batchField("auto_ban", "autoBan", "batchEditGroupRouting", "number", Number),
  batchField("limit", "usageLimit", "batchEditGroupRouting", "limit", serializeChannelLimitForPayload),
  batchField("affinity", "batchEditFieldAffinity", "batchEditGroupRouting", "affinity", parseAffinity),
  batchField("model_mapping", "modelMapping", "batchEditGroupProcessing", "modelMapping", String),
  batchField("system_prompt", "systemPrompt", "batchEditGroupProcessing", "textarea", String),
  batchField("role_mapping", "roleMapping", "batchEditGroupProcessing", "roleMapping", String),
  batchField("param_override", "paramOverride", "batchEditGroupProcessing", "textarea", String),
  batchField("header_override", "headerOverride", "batchEditGroupProcessing", "textarea", String),
  batchField("supported_api_types", "supportedApiTypes", "batchEditGroupProcessing", "input", String),
  batchField("endpoints", "sectionEndpoints", "batchEditGroupProcessing", "endpoints", String),
  batchField("passthrough_enabled", "passthroughEnabled", "batchEditGroupProcessing", "switch", Boolean),
  batchField("system_prompt_in_input", "fieldSystemPromptInInput", "batchEditGroupProcessing", "switch", Boolean),
  batchField("use_legacy_adaptor", "useLegacyAdaptor", "batchEditGroupProcessing", "switch", Boolean),
  batchField("setting", "setting", "batchEditGroupProcessing", "textarea", String),
  batchField("other_settings", "otherSettings", "batchEditGroupProcessing", "textarea", String),
  batchField("proxy_url", "proxy", "batchEditGroupConnection", "input", String),
  batchField("organization", "organization", "batchEditGroupConnection", "input", String),
  batchField("api_version", "apiVersion", "batchEditGroupConnection", "input", String),
  batchField("disable_keepalive", "disableKeepalive", "batchEditGroupConnection", "switch", Boolean),
  batchField("status_code_mapping", "statusCodeMapping", "batchEditGroupConnection", "textarea", String),
  batchField("resilience", "resilienceOverride", "batchEditGroupConnection", "resilience", parseResilience),
  batchField("price_ratio", "priceRatio", "batchEditGroupConnection", "number", Number),
  batchField("free", "free", "batchEditGroupConnection", "switch", Boolean, true),
] as const;

type RegisteredBatchField = (typeof batchFieldRegistry)[number]["key"];
type UncoveredBatchField = Exclude<BatchEditableKey, RegisteredBatchField>;
type ExtraBatchField = Exclude<RegisteredBatchField, BatchEditableKey>;
const _batchFieldRegistryCoversEveryBackendField: UncoveredBatchField extends never
  ? true
  : ["Backend batch field missing from registry", UncoveredBatchField] = true;
const _batchFieldRegistryHasNoExtraFields: ExtraBatchField extends never
  ? true
  : ["Registry field is not in backend batch DTO", ExtraBatchField] = true;
const _batchFieldRegistryHasNoDuplicates = new Set(batchFieldRegistry.map((field) => field.key)).size === batchFieldRegistry.length;
if (!_batchFieldRegistryHasNoDuplicates) throw new Error("channel batch field registry has duplicate keys");
void _batchFieldRegistryCoversEveryBackendField;
void _batchFieldRegistryHasNoExtraFields;

function createInitialState(): BatchEditState {
  return Object.fromEntries(
    batchFieldRegistry.map(({ key, initial }) => [key, {
      enabled: false,
      value: initial,
    }]),
  ) as BatchEditState;
}

export function buildBatchFields(state: BatchEditState): Partial<BatchEditableChannelFields> {
  return Object.fromEntries(
    batchFieldRegistry.flatMap((field) => buildBatchField(field, state)),
  );
}

function buildBatchField<K extends BatchEditableKey>(
  field: BatchFieldDefinition<K>,
  state: BatchEditState,
): Array<[K, BatchEditableChannelFields[K]]> {
  const current = state[field.key] as BatchFieldState<BatchFormValue<K>>;
  return current.enabled ? [[field.key, field.serialize(current.value)]] : [];
}

const MAX_BATCH_EDIT_IDS = 500;

export type BatchEditIDNormalization = { ids: number[] } | { error: "batchEditSelectAtLeastOne" | "batchEditInvalidId" | "batchEditMaxIds" };

export function normalizeBatchEditIDs(rawIDs: number[]): BatchEditIDNormalization {
  if (rawIDs.length === 0) return { error: "batchEditSelectAtLeastOne" };
  if (rawIDs.some((id) => !Number.isInteger(id) || id <= 0)) {
    return { error: "batchEditInvalidId" };
  }
  const ids = [...new Set(rawIDs)].sort((left, right) => left - right);
  if (ids.length > MAX_BATCH_EDIT_IDS) return { error: "batchEditMaxIds" };
  return { ids };
}

function normalizePublicDisplayName(raw: string): { value: string } | { error: "publicDisplayNameTooLong" | "publicDisplayNameControlCharacters" } {
  const value = raw.trim();
  const error = getPublicDisplayNameValidationError(value);
  if (error) return { error };
  return { value };
}

export interface ChannelBatchEditDialogProps {
  ids: number[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSucceeded: (response: BatchEditChannelResponse) => void;
}

export function ChannelBatchEditDialog({
  ids,
  open,
  onOpenChange,
  onSucceeded,
}: ChannelBatchEditDialogProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const batchEdit = useBatchEditChannels();
  const [state, setState] = useState<BatchEditState>(createInitialState);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const generationRef = useRef(0);
  const fields = useMemo(() => buildBatchFields(state), [state]);
  const normalizedIDs = useMemo(() => normalizeBatchEditIDs(ids), [ids]);
  const isPending = submitting;
  const targetCount = "ids" in normalizedIDs ? normalizedIDs.ids.length : ids.length;

  const reset = () => {
    setState(createInitialState());
    setError(null);
    setSubmitting(false);
  };

  useEffect(() => {
    if (!open) {
      generationRef.current += 1;
      // eslint-disable-next-line react-hooks/set-state-in-effect -- internal form state must reset when the controlled dialog closes
      reset();
    }
  }, [open]);

  const setField = <K extends BatchEditableKey>(key: K, patch: Partial<BatchFieldState<BatchFormValue<K>>>) => {
    setState((current) => ({
      ...current,
      [key]: { ...current[key], ...patch },
    }));
  };

  const setFormValue = <K extends BatchEditableKey>(key: K, value: BatchFormValue<K>) => {
    setField(key, { value });
  };

  const form = Object.fromEntries(
    batchFieldRegistry.map(({ key }) => [key, state[key].value]),
  ) as Omit<ChannelForm, "name">;
  const batchForm = { ...emptyForm, ...form };
  const setBatchForm = (next: ChannelForm) => {
    for (const { key } of batchFieldRegistry) setFormValue(key, next[key]);
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      generationRef.current += 1;
      reset();
    }
    onOpenChange(nextOpen);
  };

  const submit = async () => {
    if (ids.length === 0 || Object.keys(fields).length === 0 || isPending) return;
    if ("error" in normalizedIDs) {
      setError(t(normalizedIDs.error, { limit: MAX_BATCH_EDIT_IDS }));
      return;
    }
    const publicDisplayName = state.public_display_name;
    const normalizedPublicDisplayName = publicDisplayName.enabled
      ? normalizePublicDisplayName(publicDisplayName.value)
      : null;
    if (normalizedPublicDisplayName && "error" in normalizedPublicDisplayName) {
      setError(t(normalizedPublicDisplayName.error));
      return;
    }
    const requestFields = {
      ...fields,
      ...(normalizedPublicDisplayName && { public_display_name: normalizedPublicDisplayName.value }),
    } as NonEmptyPartial<BatchEditableChannelFields>;
    const submissionGeneration = generationRef.current;
    setSubmitting(true);
    setError(null);
    try {
      const request: BatchEditChannelRequest = { ids: normalizedIDs.ids, fields: requestFields };
      const response = await batchEdit.mutateAsync(request);
      onSucceeded(response);
      if (submissionGeneration !== generationRef.current) return;
      toast.success(t("batchEditSuccess", { count: response.updated_count }));
      handleOpenChange(false);
    } catch (cause) {
      if (submissionGeneration !== generationRef.current) return;
      const message = cause instanceof Error ? cause.message : t("batchEditFailed");
      setError(message);
      toast.error(message);
    } finally {
      if (submissionGeneration === generationRef.current) setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-h-[90dvh] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden p-0 sm:max-w-5xl">
        <DialogHeader className="px-4 pt-4 pr-12 sm:px-6 sm:pt-6 sm:pr-12">
          <DialogTitle>{t("batchEditTitle")}</DialogTitle>
          <DialogDescription>{t("batchEditDescription", { count: targetCount })}</DialogDescription>
        </DialogHeader>
        <div data-slot="channel-batch-edit-body" className="min-h-0 overflow-y-auto px-4 py-4 sm:px-6">
          {error && <p role="alert" className="mb-4 text-sm text-destructive">{error}</p>}
          <div className="flex flex-col gap-6">
            {batchFieldGroupMessageKeys.map((group) => (
              <FieldSet key={group}>
                <FieldLegend>{t(group)}</FieldLegend>
                <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  {batchFieldRegistry
                    .filter((field) => field.group === group)
                    .map((field) => (
                      <BatchField key={field.key} field={field} state={state} setField={setField} t={t}>
                        {renderFieldControl(field, batchForm, setBatchForm, setFormValue, state[field.key].enabled, `batch-edit-${field.key}-editor-label`, t)}
                      </BatchField>
                    ))}
                </FieldGroup>
              </FieldSet>
            ))}
          </div>
        </div>
        <DialogFooter className="border-t bg-background px-4 py-4 sm:px-6">
          {Object.keys(fields).length === 0 ? (
            <p role="status" className="mr-auto text-sm text-muted-foreground">{t("noBatchFieldSelected")}</p>
          ) : null}
          <Button type="button" variant="outline" onClick={() => handleOpenChange(false)}>
            {tc("cancel")}
          </Button>
          <Button type="button" onClick={submit} disabled={isPending || ids.length === 0 || Object.keys(fields).length === 0}>
            {isPending ? t("batchEditApplying") : t("batchEditApply", { count: targetCount })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function BatchField({
  field,
  state,
  setField,
  t,
  children,
}: {
  field: (typeof batchFieldRegistry)[number];
  state: BatchEditState;
  setField: <Key extends BatchEditableKey>(key: Key, patch: Partial<BatchFieldState<BatchFormValue<Key>>>) => void;
  t: ReturnType<typeof useTranslations<"channels">>;
  children: ReactNode;
}) {
  const selected = state[field.key].enabled;
  const checkboxID = `batch-edit-${field.key}`;
  const editorLabelID = `batch-edit-${field.key}-editor-label`;
  const spansBothColumns = ["modelMapping", "roleMapping", "limit", "affinity", "resilience", "endpoints"].includes(field.control);
  return (
    <Field
      orientation="horizontal"
      data-batch-field={field.key}
      data-disabled={!selected}
      className={cn(spansBothColumns && "sm:col-span-2")}
    >
      <Checkbox
        id={checkboxID}
        checked={selected}
        aria-label={t("batchEditUpdateField", { field: t(field.label) })}
        onCheckedChange={(enabled) => setField(field.key, { enabled: !!enabled })}
      />
      <FieldContent>
        <FieldLabel htmlFor={checkboxID}>{t(field.label)}</FieldLabel>
        <FieldDescription>{t("batchEditFieldHint")}</FieldDescription>
        <fieldset className="pt-2" disabled={!selected} aria-labelledby={editorLabelID}>
          <legend id={editorLabelID} className="sr-only">{t(field.label)}</legend>
          {children}
        </fieldset>
      </FieldContent>
    </Field>
  );
}

function renderFieldControl<K extends BatchEditableKey>(
  field: BatchFieldDefinition<K>,
  form: ChannelForm,
  setForm: (next: ChannelForm) => void,
  setValue: <Key extends BatchEditableKey>(key: Key, value: BatchFormValue<Key>) => void,
  enabled: boolean,
  editorLabelledBy: string,
  t: ReturnType<typeof useTranslations<"channels">>,
): ReactNode {
  const { key } = field;
  const input = (type = "text") => (
    <Input
      type={type}
      className={cn(type === "number" && "font-mono tabular-nums")}
      aria-label={t(field.label)}
      value={String(form[key])}
      onChange={(event) => setValue(key, event.target.value as BatchFormValue<K>)}
    />
  );
  const toggle = () => (
    <Switch
      aria-label={t(field.label)}
      checked={Boolean(form[key])}
      onCheckedChange={(checked) => setValue(key, checked as BatchFormValue<K>)}
    />
  );

  if (key === "public_display_name") {
    return (
      <div className="flex flex-col gap-2">
        {input()}
        {!form.public_display_name.trim() ? (
          <p className="text-xs text-muted-foreground">{t("publicDisplayNameAutoPreview")}</p>
        ) : null}
      </div>
    );
  }

  if (key === "price_ratio") {
    return (
      <NumberUnitInput
        type="number"
        aria-label={t(field.label)}
        value={String(form[key])}
        humanReadable={humanizeNumberUnit(String(form[key]), "ratio")}
        onChange={(event) => setValue(key, event.target.value as BatchFormValue<K>)}
      />
    );
  }

  switch (field.control) {
    case "status":
      return <StatusSelect value={form.status} showLabel={false} labelledBy={editorLabelledBy} onChange={(value) => setValue(key, value as BatchFormValue<K>)} />;
    case "modelMapping":
      return <ModelMappingInput value={form.model_mapping} onChange={(value) => setValue(key, value as BatchFormValue<K>)} />;
    case "roleMapping":
      return <RoleMappingEditor value={form.role_mapping} onChange={(value) => setValue(key, value as BatchFormValue<K>)} />;
    case "limit":
      return <LimitRulesEditor limit={parseLimit(form.limit)} onChange={(value) => setValue(key, stringifyLimit(value) as BatchFormValue<K>)} />;
    case "affinity":
      return <AffinitySection form={form} setForm={setForm} />;
    case "resilience":
      return <ResilienceSection form={form} setForm={setForm} />;
    case "endpoints":
      return (
        <ChannelEndpointsEditor
          endpoints={form.endpoints}
          baseURL={form.base_url}
          showEmptyWarning={enabled}
          onEndpointsChange={(endpoints) => setValue(key, endpoints as BatchFormValue<K>)}
        />
      );
    case "switch":
      return toggle();
    case "textarea":
      return (
        <Textarea
          aria-label={t(field.label)}
          value={form[key] as string}
          onChange={(event) => setValue(key, event.target.value as BatchFormValue<K>)}
        />
      );
    case "number":
      return input("number");
    default:
      return input();
  }
}
