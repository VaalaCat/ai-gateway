"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import { ModelSelector } from "@/components/business/model-selector";
import { FieldTip } from "@/components/business/field-tip";
import { CodeEditor } from "@/components/script/code-editor";
import { ScriptApiReference } from "@/components/script/api-reference";
import { useScript, useCreateScript, useUpdateScript } from "@/lib/api/scripts";
import { formatErrorToast } from "@/lib/api/error-toast";

type Mode = { kind: "create" } | { kind: "edit"; id: number };

const DEFAULT_CODE = "function onRequest(ctx) {\n  // ctx.body 可直接改\n}\n";

// 配置区的 mono 小标签——强化"编辑一份配置/代码"的开发者气质。
function FieldLabel({ children }: { children: React.ReactNode }) {
  return (
    <Label className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
      {children}
    </Label>
  );
}

export function ScriptForm({ mode }: { mode: Mode }) {
  const t = useTranslations("scripts");
  const router = useRouter();
  const createMut = useCreateScript();
  const updateMut = useUpdateScript();
  const { data: existing } = useScript(mode.kind === "edit" ? mode.id : 0);

  const [name, setName] = useState("");
  const [code, setCode] = useState(DEFAULT_CODE);
  const [enabled, setEnabled] = useState(true);
  const [priority, setPriority] = useState("0");
  const [channelIds, setChannelIds] = useState<number[]>([]);
  const [modelNames, setModelNames] = useState<string[]>([]);

  // prefilled 守卫：只在首次拿到 existing 时回填一次，避免后台 refetch
  // （staleTime/refetchOnWindowFocus）用服务端旧值覆盖用户正在编辑的输入。
  const prefilled = useRef(false);
  useEffect(() => {
    if (mode.kind === "edit" && existing && !prefilled.current) {
      prefilled.current = true;
      setName(existing.name);
      setCode(existing.code);
      setEnabled(existing.enabled);
      setPriority(String(existing.priority));
      setChannelIds(existing.scope?.channel_ids ?? []);
      setModelNames(existing.scope?.model_names ?? []);
    }
  }, [mode.kind, existing]);

  const pending = createMut.isPending || updateMut.isPending;

  const submit = async () => {
    const body = {
      name,
      code,
      enabled,
      priority: Number(priority) || 0,
      scope: { channel_ids: channelIds, model_names: modelNames },
    };
    try {
      if (mode.kind === "edit") {
        await updateMut.mutateAsync({ id: mode.id, ...body });
      } else {
        await createMut.mutateAsync(body);
      }
      toast.success(t("saved"));
      router.push("/scripts");
    } catch (e) {
      toast.error(formatErrorToast(e, t("save")));
    }
  };

  return (
    <div className="space-y-6">
      {/* 紧凑 meta/作用域：名称 / 优先级 / 启用 / 频道 / 模型 一行（移动端堆叠） */}
      <div className="rounded-lg border bg-card p-4 shadow-sm">
        <div className="flex flex-col gap-4 md:flex-row md:flex-wrap md:items-end">
          <div className="grid gap-1.5 md:min-w-[14rem] md:flex-1">
            <FieldLabel>{t("name")}</FieldLabel>
            <div className="relative">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="trim-temperature"
                className="pr-9 font-mono"
              />
              <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-muted-foreground">
                .js
              </span>
            </div>
          </div>

          <div className="flex gap-4">
            <div className="grid w-24 gap-1.5">
              <FieldLabel>{t("priority")}</FieldLabel>
              <Input
                type="number"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
                className="text-center font-mono tabular-nums"
              />
            </div>
            <div className="grid gap-1.5">
              <FieldLabel>{t("status")}</FieldLabel>
              <label className="flex h-9 cursor-pointer items-center gap-2 rounded-md border bg-background px-3">
                <Switch checked={enabled} onCheckedChange={setEnabled} />
                <span className="font-mono text-xs text-muted-foreground">
                  {enabled ? t("enabled") : t("disabled")}
                </span>
              </label>
            </div>
          </div>

          <div className="grid gap-1.5 md:min-w-[12rem] md:flex-1">
            <FieldLabel>
              {t("scopeChannels")}
              <FieldTip text={t("scopeHint")} />
            </FieldLabel>
            <EntityMultiPicker
              entity="channel"
              value={channelIds.map(String)}
              onChange={(vals) => setChannelIds(vals.map(Number))}
              placeholder={t("selectChannels")}
            />
          </div>

          <div className="grid gap-1.5 md:min-w-[12rem] md:flex-1">
            <FieldLabel>{t("scopeModels")}</FieldLabel>
            <ModelSelector mode="multi" value={modelNames} onChange={setModelNames} placeholder={t("selectModels")} />
          </div>
        </div>
      </div>

      {/* 源码 */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
            {t("code")}
          </span>
          <span className="h-px flex-1 bg-border" />
        </div>
        <p className="text-xs leading-relaxed text-muted-foreground">{t("codeHint")}</p>
        <CodeEditor value={code} onChange={setCode} filename={name} />
        <ScriptApiReference />
      </div>

      {/* 操作 */}
      <div className="flex justify-end gap-2 border-t pt-4">
        <Button variant="outline" onClick={() => router.push("/scripts")}>
          {t("cancel")}
        </Button>
        <Button onClick={submit} disabled={pending || !name.trim()}>
          {pending && <Loader2 className="mr-2 size-4 animate-spin" />}
          {t("save")}
        </Button>
      </div>
    </div>
  );
}
