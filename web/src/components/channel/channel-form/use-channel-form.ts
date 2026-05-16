"use client";

import { useReducer, useCallback, useEffect } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useTranslations } from "next-intl";
import { useChannel, useCreateChannel, useUpdateChannel } from "@/lib/api/channels";
import type { Channel } from "@/lib/types";
import { ChannelForm, emptyForm } from "./types";
import { mapChannelToForm, sanitizeOtherSettingsForSubmit } from "./utils";

export type ChannelFormMode =
  | { kind: "create" }
  | { kind: "edit"; id: number };

export interface UseChannelFormResult {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  initial: ChannelForm;
  isDirty: boolean;
  dirtyFieldCount: number;
  isLoading: boolean;
  notFound: boolean;
  saving: boolean;
  submit: () => Promise<void>;
  cancel: () => void;
}

interface FormState {
  form: ChannelForm;
  initial: ChannelForm;
  /** The channel id for which `initial` was set; used to detect channel change. */
  loadedChannelId: number | null;
}

type FormAction =
  | { type: "SET_FORM"; form: ChannelForm }
  | { type: "LOAD_CHANNEL"; channel: Channel }
  | { type: "CLEAR_DIRTY" };

function formReducer(state: FormState, action: FormAction): FormState {
  switch (action.type) {
    case "SET_FORM":
      return { ...state, form: action.form };
    case "LOAD_CHANNEL": {
      if (state.loadedChannelId === action.channel.id) return state;
      const f = mapChannelToForm(action.channel);
      return { form: f, initial: f, loadedChannelId: action.channel.id };
    }
    case "CLEAR_DIRTY":
      return { ...state, initial: state.form };
    default:
      return state;
  }
}

function shallowEqual(a: ChannelForm, b: ChannelForm): boolean {
  const keys = Object.keys(a) as Array<keyof ChannelForm>;
  for (const k of keys) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

function diffFieldCount(a: ChannelForm, b: ChannelForm): number {
  let n = 0;
  const keys = Object.keys(a) as Array<keyof ChannelForm>;
  for (const k of keys) {
    if (a[k] !== b[k]) n++;
  }
  return n;
}

export function useChannelForm(mode: ChannelFormMode): UseChannelFormResult {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const router = useRouter();

  const editId = mode.kind === "edit" ? mode.id : 0;
  const { data: channel, isLoading, isError } = useChannel(editId);
  const notFound = mode.kind === "edit" && !isLoading && (isError || channel === undefined);

  const [state, dispatch] = useReducer(formReducer, {
    form: emptyForm,
    initial: emptyForm,
    loadedChannelId: null,
  });

  // Sync when channel data arrives. useReducer dispatch is allowed in effects.
  useEffect(() => {
    if (mode.kind === "edit" && channel) {
      dispatch({ type: "LOAD_CHANNEL", channel });
    }
  }, [mode.kind, channel]);

  const { form, initial } = state;
  const isDirty = !shallowEqual(form, initial);
  const dirtyFieldCount = diffFieldCount(form, initial);

  const setForm = useCallback((next: ChannelForm) => {
    dispatch({ type: "SET_FORM", form: next });
  }, []);

  const createMutation = useCreateChannel();
  const updateMutation = useUpdateChannel();

  // Build payload identically to current channels/page.tsx submit logic.
  const buildPayload = useCallback((): Partial<Channel> => {
    const otherSettings = sanitizeOtherSettingsForSubmit(form.other_settings, form.endpoints);
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
      proxy_url: form.proxy_url,
      role_mapping: form.role_mapping,
    } as Partial<Channel>;
  }, [form]);

  const submit = useCallback(async () => {
    try {
      const payload = buildPayload();
      if (mode.kind === "create") {
        await createMutation.mutateAsync(payload);
        toast.success(t("createSuccess"));
        router.push("/channels");
      } else {
        await updateMutation.mutateAsync({ id: mode.id, ...payload });
        toast.success(t("updateSuccess"));
        dispatch({ type: "CLEAR_DIRTY" }); // clear dirty after save
      }
    } catch {
      toast.error(tc("error"));
    }
  }, [mode, buildPayload, createMutation, updateMutation, router, t, tc]);

  const cancel = useCallback(() => {
    if (isDirty && !window.confirm(t("cancelDirtyConfirm"))) return;
    router.push("/channels");
  }, [isDirty, router, t]);

  // onbeforeunload guard for browser-level navigation.
  useEffect(() => {
    if (!isDirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [isDirty]);

  return {
    form,
    setForm,
    initial,
    isDirty,
    dirtyFieldCount,
    isLoading: mode.kind === "edit" ? isLoading : false,
    notFound,
    saving: createMutation.isPending || updateMutation.isPending,
    submit,
    cancel,
  };
}
