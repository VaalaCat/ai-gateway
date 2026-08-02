// web/src/components/channel/channel-form/adapters/admin.ts
import {
  useChannel,
  useCreateChannel,
  useUpdateChannel,
  useChannelTypes,
} from "@/lib/api/channels";
import type { Channel } from "@/lib/types";
import type { ChannelFormAdapter } from "../adapter";
import { mapChannelToForm, sanitizeOtherSettingsForSubmit, parseResilience, serializeChannelLimitForPayload, parseAffinity } from "../utils";
import type { ChannelForm } from "../types";

function buildPayload(
  form: ChannelForm,
  opts: { includeEmptyResilience?: boolean; includeEmptyAffinity?: boolean } = {},
): Partial<Channel> {
  const otherSettings = sanitizeOtherSettingsForSubmit(
    form.other_settings,
    form.endpoints,
  );
  const resilienceRaw = parseResilience(form.resilience);
  const hasResilience = Object.keys(resilienceRaw).length > 0;
  const affinityRaw = parseAffinity(form.affinity);
  const hasAffinity = Object.keys(affinityRaw).length > 0;
  const limit = serializeChannelLimitForPayload(form.limit);
  return {
    name: form.name,
    public_display_name: form.public_display_name,
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
    ...((hasAffinity || opts.includeEmptyAffinity) && { affinity: affinityRaw }),
    limit,
  } as Partial<Channel>;
}

export function buildAdminCreatePayload(form: ChannelForm): Partial<Channel> {
  return buildPayload(form);
}

export function buildAdminUpdatePayload(
  form: ChannelForm,
  initial: ChannelForm,
): { fields: Record<string, unknown> } {
  const { name: _name, ...fields } = buildPayload(form, {
    includeEmptyResilience: form.resilience !== initial.resilience,
    includeEmptyAffinity: form.affinity !== initial.affinity,
  });
  return { fields };
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
  buildCreatePayload: buildAdminCreatePayload,
  buildUpdatePayload: buildAdminUpdatePayload,
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
