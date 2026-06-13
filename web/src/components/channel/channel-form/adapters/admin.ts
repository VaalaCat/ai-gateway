// web/src/components/channel/channel-form/adapters/admin.ts
import {
  useChannel,
  useCreateChannel,
  useUpdateChannel,
  useChannelTypes,
} from "@/lib/api/channels";
import type { Channel } from "@/lib/types";
import type { ChannelFormAdapter } from "../adapter";
import { mapChannelToForm, sanitizeOtherSettingsForSubmit, parseResilience, parseLimit } from "../utils";
import type { ChannelForm } from "../types";

function buildPayload(
  form: ChannelForm,
  opts: { includeEmptyResilience?: boolean } = {},
): Partial<Channel> {
  const otherSettings = sanitizeOtherSettingsForSubmit(
    form.other_settings,
    form.endpoints,
  );
  const resilienceRaw = parseResilience(form.resilience);
  const hasResilience = Object.keys(resilienceRaw).length > 0;
  const limitRaw = parseLimit(form.limit);
  const cleanRules = (limitRaw.rules ?? []).filter((r) => r.threshold > 0);
  const hasCutoff = typeof limitRaw.disable_at === "number" && limitRaw.disable_at > 0;
  // 始终发送 limit:空对象表示清空(后端 partial update 才能真正清掉旧配置);
  // 同时丢弃 threshold<=0 的半成品规则,避免误存 "0 即永久禁用" 的规则。
  const limit = {
    ...(hasCutoff ? { disable_at: limitRaw.disable_at } : {}),
    ...(cleanRules.length > 0 ? { rules: cleanRules } : {}),
  };
  return {
    name: form.name,
    type: Number(form.type),
    key: form.key,
    base_url: form.base_url,
    models: form.models,
    model_mapping: form.model_mapping,
    weight: Number(form.weight),
    priority: Number(form.priority),
    status: Number(form.status),
    setting: form.setting,
    organization: form.organization,
    api_version: form.api_version,
    tag: form.tag,
    remark: form.remark,
    test_model: form.test_model,
    auto_ban: Number(form.auto_ban),
    status_code_mapping: form.status_code_mapping,
    param_override: form.param_override,
    header_override: form.header_override,
    other_settings: otherSettings,
    supported_api_types: form.supported_api_types,
    endpoints: form.endpoints,
    passthrough_enabled: form.passthrough_enabled,
    use_legacy_adaptor: form.use_legacy_adaptor,
    system_prompt: form.system_prompt,
    system_prompt_in_input: form.system_prompt_in_input,
    proxy_url: form.proxy_url,
    role_mapping: form.role_mapping,
    disable_keepalive: form.disable_keepalive,
    price_ratio: Number(form.price_ratio),
    free: form.free,
    ...((hasResilience || opts.includeEmptyResilience) && { resilience: resilienceRaw }),
    limit,
  } as Partial<Channel>;
}

export const adminChannelAdapter: ChannelFormAdapter<Channel> = {
  listPath: "/channels",
  scriptsHref: "/scripts",
  mapEntityToForm: mapChannelToForm,
  mapEntityToCopyForm: (c) => ({
    ...mapChannelToForm(c),
    name: `${c.name ?? ""}-copy`,
    key: c.key ?? "",
  }),
  buildCreatePayload: (form) => buildPayload(form),
  buildUpdatePayload: (form, initial) => ({
    fields: buildPayload(form, {
      includeEmptyResilience: form.resilience !== initial.resilience,
    }) as Record<string, unknown>,
  }),
  useEntity: (id) => {
    const q = useChannel(id);
    return { data: q.data, isLoading: q.isLoading, isError: q.isError };
  },
  useCreate: () => {
    const m = useCreateChannel();
    return {
      mutateAsync: (p) => m.mutateAsync(p as Partial<Channel>),
      isPending: m.isPending,
    };
  },
  useUpdate: () => {
    const m = useUpdateChannel();
    return {
      mutateAsync: ({ id, fields }) =>
        m.mutateAsync({ id, ...(fields as Partial<Channel>) }),
      isPending: m.isPending,
    };
  },
  useTypes: () => useChannelTypes(),
};
