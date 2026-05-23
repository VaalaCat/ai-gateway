// web/src/components/channel/channel-form/adapters/byok.ts
import {
  useBYOKChannel,
  useBYOKSupportedTypes,
  useBYOKAvailableModels,
  useCreateBYOKChannel,
  useUpdateBYOKChannel,
  useUpdateBYOKChannelKey,
  useTestBYOKChannel,
  type BYOKChannelDetail,
} from "@/lib/api/byok-channels";
import type { ChannelFormAdapter } from "../adapter";
import { type ChannelForm, emptyForm } from "../types";

function csvToArray(csv: string): string[] {
  return csv
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

function parseModelMappingJson(s: string): Record<string, string> {
  if (!s) return {};
  try {
    const obj = JSON.parse(s);
    if (obj && typeof obj === "object" && !Array.isArray(obj)) {
      return Object.fromEntries(
        Object.entries(obj).filter(([, v]) => typeof v === "string"),
      ) as Record<string, string>;
    }
  } catch {
    // ignore — malformed JSON shouldn't crash submit
  }
  return {};
}

function mapBYOKToForm(pc: BYOKChannelDetail): ChannelForm {
  return {
    ...emptyForm,
    name: pc.name ?? "",
    type: String(pc.type ?? 1),
    key: "", // never pre-fill plaintext key; last4 shown via keyFieldHelpText
    base_url: pc.base_url ?? "",
    models: (pc.models ?? []).join(","),
    model_mapping: pc.model_mapping ? JSON.stringify(pc.model_mapping) : "",
    weight: String(pc.weight ?? 1),
    priority: String(pc.priority ?? 0),
    status: String(pc.status ?? 1),
    organization: pc.organization ?? "",
    api_version: pc.api_version ?? "",
    system_prompt: pc.system_prompt ?? "",
    system_prompt_in_input: !!pc.system_prompt_in_input,
    role_mapping: pc.role_mapping ?? "",
    param_override: pc.param_override ?? "",
    setting: pc.setting ?? "",
    tag: pc.tag ?? "",
    remark: pc.remark ?? "",
    test_model: pc.test_model ?? "",
    auto_ban: String(pc.auto_ban ?? 0),
    status_code_mapping: pc.status_code_mapping ?? "",
    other_settings: pc.other_settings ?? "",
    supported_api_types: pc.supported_api_types ?? "",
    endpoints: pc.endpoints ?? "",
    passthrough_enabled: !!pc.passthrough_enabled,
    use_legacy_adaptor: false,
    proxy_url: "",
    header_override: "",
  };
}

function buildBYOKCreate(form: ChannelForm) {
  return {
    name: form.name,
    type: Number(form.type),
    key: form.key,
    base_url: form.base_url || undefined,
    models: csvToArray(form.models),
    model_mapping: parseModelMappingJson(form.model_mapping),
    weight: Number(form.weight || 1),
    priority: Number(form.priority || 0),
    supported_api_types: form.supported_api_types,
    endpoints: form.endpoints,
    organization: form.organization,
    api_version: form.api_version,
    system_prompt: form.system_prompt,
    system_prompt_in_input: form.system_prompt_in_input,
    role_mapping: form.role_mapping,
    param_override: form.param_override,
    setting: form.setting,
    tag: form.tag,
    remark: form.remark,
    test_model: form.test_model,
    auto_ban: Number(form.auto_ban || 0),
    status_code_mapping: form.status_code_mapping,
    other_settings: form.other_settings,
    passthrough_enabled: form.passthrough_enabled,
  };
}

function buildBYOKUpdate(form: ChannelForm, initial: ChannelForm) {
  const fields: Record<string, unknown> = {};
  const stringFields: Array<keyof ChannelForm> = [
    "name",
    "base_url",
    "supported_api_types",
    "endpoints",
    "organization",
    "api_version",
    "system_prompt",
    "role_mapping",
    "param_override",
    "setting",
    "tag",
    "remark",
    "test_model",
    "status_code_mapping",
    "other_settings",
  ];
  for (const f of stringFields) {
    if (form[f] !== initial[f]) fields[f] = form[f];
  }
  const numberFields: Array<keyof ChannelForm> = [
    "type",
    "weight",
    "priority",
    "status",
    "auto_ban",
  ];
  for (const f of numberFields) {
    if (form[f] !== initial[f]) fields[f] = Number(form[f]);
  }
  if (form.passthrough_enabled !== initial.passthrough_enabled) {
    fields.passthrough_enabled = form.passthrough_enabled;
  }
  if (form.system_prompt_in_input !== initial.system_prompt_in_input) {
    fields.system_prompt_in_input = form.system_prompt_in_input;
  }
  if (form.models !== initial.models) {
    fields.models = csvToArray(form.models);
  }
  if (form.model_mapping !== initial.model_mapping) {
    fields.model_mapping = parseModelMappingJson(form.model_mapping);
  }
  const rotateKey = form.key ? form.key : undefined;
  return { fields, rotateKey };
}

const HIDDEN: ReadonlySet<keyof ChannelForm> = new Set<keyof ChannelForm>([
  "proxy_url",
  "header_override",
  "use_legacy_adaptor",
]);

export const byokChannelAdapter: ChannelFormAdapter<BYOKChannelDetail> = {
  listPath: "/byok",
  hiddenFields: HIDDEN,
  keyFieldHelpText: (entity) =>
    entity
      ? `末 4 位: ${entity.key_last4 ?? "—"}，未输入则不变更`
      : undefined,
  mapEntityToForm: mapBYOKToForm,
  buildCreatePayload: buildBYOKCreate,
  buildUpdatePayload: buildBYOKUpdate,
  useEntity: (id) => {
    const q = useBYOKChannel(id);
    return { data: q.data, isLoading: q.isLoading, isError: q.isError };
  },
  useCreate: () => {
    const m = useCreateBYOKChannel();
    return {
      mutateAsync: (p) =>
        m.mutateAsync(p as Parameters<typeof m.mutateAsync>[0]),
      isPending: m.isPending,
    };
  },
  useUpdate: () => {
    const m = useUpdateBYOKChannel();
    return {
      mutateAsync: ({ id, fields }) =>
        m.mutateAsync({
          id,
          ...fields,
        } as Parameters<typeof m.mutateAsync>[0]),
      isPending: m.isPending,
    };
  },
  useRotateKey: () => {
    const m = useUpdateBYOKChannelKey();
    return { mutateAsync: m.mutateAsync, isPending: m.isPending };
  },
  useTypes: () => {
    const q = useBYOKSupportedTypes();
    const types = (q.data?.types ?? []).map((t) => ({
      id: t.id,
      name: t.name,
      i18n_key: "", // BYOK backend has no i18n_key; ChannelTypeMeta requires it
    }));
    return { data: types };
  },
  useModelsCatalog: () => {
    const q = useBYOKAvailableModels();
    return { data: q.data?.models };
  },
  useTestChannel: () => {
    const m = useTestBYOKChannel();
    return {
      ...m,
      mutateAsync: async (args: { id: number; model?: string; endpoint_type?: string }) => {
        const raw = await m.mutateAsync(args);
        return {
          ok: raw.ok,
          status_code: raw.status_code,
          detail: raw.detail,
          latency_ms: raw.latency_ms,
        };
      },
    };
  },
};
