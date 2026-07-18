"use client";

import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  type ModelRoutingOwner,
  useModelRouting,
  useCreateModelRouting,
  useUpdateModelRouting,
} from "@/lib/api/model-routings";
import { routingFormSchema, RoutingFormValues, FormMode } from "./types";

export function useRoutingForm(
  mode: FormMode,
  apiMode: "admin" | "user" = "admin",
  owner: ModelRoutingOwner = { kind: "scope" },
) {
  const form = useForm<RoutingFormValues>({
    resolver: zodResolver(routingFormSchema),
    defaultValues: {
      name: "",
      scope: owner.kind === "token" ? "token" : apiMode === "user" ? "user" : "global",
      user_id: 0,
      members: [],
      enabled: true,
      remark: "",
    },
  });

  const id = mode.kind === "edit" ? mode.id : null;
  const { data: existing, isLoading } = useModelRouting(id, apiMode, owner);

  useEffect(() => {
    if (existing) {
      // 后端 members 字段在 JSON 响应里是字符串（GORM text 列），编辑前解析为数组
      const members = (() => {
        const raw = existing.members as unknown;
        if (Array.isArray(raw)) return raw;
        if (typeof raw === "string") {
          try { return JSON.parse(raw); } catch { return []; }
        }
        return [];
      })();
      form.reset({
        name: existing.name,
        scope: existing.scope,
        user_id: existing.user_id,
        members,
        enabled: existing.enabled,
        remark: existing.remark ?? "",
      });
    }
  }, [existing, form]);

  const createMut = useCreateModelRouting(apiMode, owner);
  const updateMut = useUpdateModelRouting(apiMode, owner);

  return { form, isLoading, createMut, updateMut, existing };
}
