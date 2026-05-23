// web/src/components/channel/channel-form/adapters/admin.ts
import {
  useChannel,
  useCreateChannel,
  useUpdateChannel,
  useChannelTypes,
} from "@/lib/api/channels";
import type { Channel } from "@/lib/types";
import type { ChannelFormAdapter } from "../adapter";
import { mapChannelToForm, sanitizeOtherSettingsForSubmit } from "../utils";
import type { ChannelForm } from "../types";

function buildPayload(form: ChannelForm): Partial<Channel> {
  const otherSettings = sanitizeOtherSettingsForSubmit(
    form.other_settings,
    form.endpoints,
  );
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
  } as Partial<Channel>;
}

export const adminChannelAdapter: ChannelFormAdapter<Channel> = {
  listPath: "/channels",
  mapEntityToForm: mapChannelToForm,
  buildCreatePayload: (form) => buildPayload(form),
  buildUpdatePayload: (form) => ({
    fields: buildPayload(form) as Record<string, unknown>,
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
