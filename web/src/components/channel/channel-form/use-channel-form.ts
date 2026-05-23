"use client";

import { useReducer, useCallback, useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useTranslations } from "next-intl";
import { formatErrorToast } from "@/lib/api/error-toast";
import { ChannelForm, emptyForm } from "./types";
import type { ChannelFormAdapter } from "./adapter";

export type ChannelFormMode =
  | { kind: "create" }
  | { kind: "edit"; id: number };

export interface UseChannelFormResult<Entity> {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
  initial: ChannelForm;
  entity: Entity | null;
  isDirty: boolean;
  dirtyFieldCount: number;
  isLoading: boolean;
  notFound: boolean;
  saving: boolean;
  submit: () => Promise<void>;
  cancel: () => void;
}

interface FormState<Entity> {
  form: ChannelForm;
  initial: ChannelForm;
  entity: Entity | null;
  /** The entity id for which `initial` was set; used to detect entity change. */
  loadedEntityId: number | null;
}

type FormAction<Entity> =
  | { type: "SET_FORM"; form: ChannelForm }
  | { type: "LOAD_ENTITY"; entity: Entity; id: number }
  | { type: "CLEAR_DIRTY" };

function makeReducer<Entity>(mapEntityToForm: (e: Entity) => ChannelForm) {
  return function formReducer(
    state: FormState<Entity>,
    action: FormAction<Entity>,
  ): FormState<Entity> {
    switch (action.type) {
      case "SET_FORM":
        return { ...state, form: action.form };
      case "LOAD_ENTITY": {
        if (state.loadedEntityId === action.id) return state;
        const f = mapEntityToForm(action.entity);
        return {
          form: f,
          initial: f,
          entity: action.entity,
          loadedEntityId: action.id,
        };
      }
      case "CLEAR_DIRTY":
        return { ...state, initial: state.form };
      default:
        return state;
    }
  };
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

export function useChannelForm<Entity>(
  mode: ChannelFormMode,
  adapter: ChannelFormAdapter<Entity>,
): UseChannelFormResult<Entity> {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const router = useRouter();

  const editId = mode.kind === "edit" ? mode.id : 0;
  const { data: entityData, isLoading, isError } = adapter.useEntity(editId);
  const notFound =
    mode.kind === "edit" && !isLoading && (isError || entityData === undefined);

  // adapter.mapEntityToForm is a module-level stable reference; useMemo keeps
  // the reducer identity stable across renders so React doesn't re-bind it.
  const reducer = useMemo(
    () => makeReducer<Entity>(adapter.mapEntityToForm),
    [adapter.mapEntityToForm],
  );
  const [state, dispatch] = useReducer(reducer, {
    form: emptyForm,
    initial: emptyForm,
    entity: null,
    loadedEntityId: null,
  } as FormState<Entity>);

  // Sync when entity data arrives. useReducer dispatch is allowed in effects.
  useEffect(() => {
    if (mode.kind === "edit" && entityData) {
      dispatch({ type: "LOAD_ENTITY", entity: entityData, id: editId });
    }
  }, [mode.kind, entityData, editId]);

  const { form, initial, entity } = state;
  const isDirty = !shallowEqual(form, initial);
  const dirtyFieldCount = diffFieldCount(form, initial);

  const setForm = useCallback((next: ChannelForm) => {
    dispatch({ type: "SET_FORM", form: next });
  }, []);

  const createMutation = adapter.useCreate();
  const updateMutation = adapter.useUpdate();
  const rotateMutation = adapter.useRotateKey?.();

  const submit = useCallback(async () => {
    try {
      if (mode.kind === "create") {
        await createMutation.mutateAsync(adapter.buildCreatePayload(form));
        toast.success(t("createSuccess"));
        router.push(adapter.listPath);
        return;
      }
      const { fields, rotateKey } = adapter.buildUpdatePayload(form, initial);
      if (rotateKey && rotateMutation) {
        await rotateMutation.mutateAsync({ id: mode.id, key: rotateKey });
      }
      if (Object.keys(fields).length > 0) {
        await updateMutation.mutateAsync({ id: mode.id, fields });
      }
      toast.success(t("updateSuccess"));
      dispatch({ type: "CLEAR_DIRTY" });
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  }, [
    mode,
    adapter,
    form,
    initial,
    createMutation,
    updateMutation,
    rotateMutation,
    router,
    t,
    tc,
  ]);

  const cancel = useCallback(() => {
    if (isDirty && !window.confirm(t("cancelDirtyConfirm"))) return;
    router.push(adapter.listPath);
  }, [isDirty, router, t, adapter.listPath]);

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
    entity,
    isDirty,
    dirtyFieldCount,
    isLoading: mode.kind === "edit" ? isLoading : false,
    notFound,
    saving:
      createMutation.isPending ||
      updateMutation.isPending ||
      !!rotateMutation?.isPending,
    submit,
    cancel,
  };
}
