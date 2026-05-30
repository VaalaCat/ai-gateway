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

// BYOK 持久化字段：buildBYOKCreate/buildBYOKUpdate 实际写给后端的字段集合。
const BYOK_PERSISTED_FIELDS = [
  "name", "type", "key", "base_url", "models", "model_mapping", "weight",
  "priority", "status", "supported_api_types", "endpoints", "organization",
  "api_version", "system_prompt", "system_prompt_in_input", "role_mapping",
  "param_override", "setting", "tag", "remark", "test_model", "auto_ban",
  "status_code_mapping", "other_settings", "passthrough_enabled",
] as const satisfies ReadonlyArray<keyof ChannelForm>;

// BYOK 隐藏字段：不持久化、表单中不渲染编辑入口。
const HIDDEN_FIELDS = [
  "proxy_url", "header_override", "use_legacy_adaptor", "price_ratio", "free",
  "disable_keepalive", "resilience", "limit",
] as const satisfies ReadonlyArray<keyof ChannelForm>;

const HIDDEN: ReadonlySet<keyof ChannelForm> = new Set(HIDDEN_FIELDS);

// 编译期一致性守卫：persisted ∪ hidden 必须覆盖全部 ChannelForm 字段。
// 新增字段若既不持久化也不隐藏，Uncovered 非 never，下行类型检查失败（pnpm build 报错），
// 强制开发者显式归类，杜绝「表单可见却被 adapter 丢弃」的假输入 bug。
type Uncovered = Exclude<
  keyof ChannelForm,
  (typeof BYOK_PERSISTED_FIELDS)[number] | (typeof HIDDEN_FIELDS)[number]
>;
const _byokFieldCoverage: Uncovered extends never
  ? true
  : ["BYOK field neither persisted nor hidden:", Uncovered] = true;
void _byokFieldCoverage;

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
