"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FieldTip } from "@/components/business/field-tip";
import { BindingEditor } from "@/components/rate-limiter/binding-editor";

import {
  useRateLimiters,
  useCreateRateLimiter,
  useUpdateRateLimiter,
} from "@/lib/api/rate-limiters";
import { formatErrorToast } from "@/lib/api/error-toast";
import type {
  RequestLimiter,
  LimiterMetric,
  LimiterKeyBy,
  LimiterAction,
  LimiterChannelScope,
} from "@/lib/types";

type Mode = { kind: "create" } | { kind: "edit"; id: number };

const METRICS: LimiterMetric[] = ["concurrency", "rate"];
const KEY_BYS: LimiterKeyBy[] = [
  "shared",
  "per_user",
  "per_group",
  "per_channel",
  "per_channel_user",
];
const ACTIONS: LimiterAction[] = ["reject", "wait"];
const CHANNEL_SCOPES: LimiterChannelScope[] = ["admin", "private", "all"];

// channelKeyed 报告该 key_by 是否依赖具体渠道，决定 channel_scope 字段是否有意义。
function channelKeyed(keyBy: LimiterKeyBy): boolean {
  return keyBy === "per_channel" || keyBy === "per_channel_user";
}

function metricOptionKey(m: LimiterMetric): "metricConcurrency" | "metricRate" {
  return m === "rate" ? "metricRate" : "metricConcurrency";
}

function keyByOptionKey(
  k: LimiterKeyBy,
):
  | "keyShared"
  | "keyPerUser"
  | "keyPerGroup"
  | "keyPerChannel"
  | "keyPerChannelUser" {
  switch (k) {
    case "per_user":
      return "keyPerUser";
    case "per_group":
      return "keyPerGroup";
    case "per_channel":
      return "keyPerChannel";
    case "per_channel_user":
      return "keyPerChannelUser";
    default:
      return "keyShared";
  }
}

function actionOptionKey(a: LimiterAction): "actionReject" | "actionWait" {
  return a === "wait" ? "actionWait" : "actionReject";
}

function scopeOptionKey(
  s: LimiterChannelScope,
): "scopeAdmin" | "scopePrivate" | "scopeAll" {
  switch (s) {
    case "private":
      return "scopePrivate";
    case "all":
      return "scopeAll";
    default:
      return "scopeAdmin";
  }
}

// 配置区的小标签，复用脚本表单的开发者气质。
function FieldLabel({
  children,
  tip,
}: {
  children: React.ReactNode;
  tip?: string;
}) {
  return (
    <Label className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
      {children}
      {tip ? <FieldTip text={tip} /> : null}
    </Label>
  );
}

export function RateLimiterForm({ mode }: { mode: Mode }) {
  const t = useTranslations("rateLimiters");
  const tc = useTranslations("common");
  const router = useRouter();

  const createMut = useCreateRateLimiter();
  const updateMut = useUpdateRateLimiter();

  // 没有 GET-by-id 端点：编辑态从列表里捞当前这条回填。
  const { data: listData } = useRateLimiters(
    { page: 1, page_size: 1000 },
    { enabled: mode.kind === "edit" },
  );
  const existing = useMemo<RequestLimiter | undefined>(() => {
    if (mode.kind !== "edit") return undefined;
    return listData?.data?.find((l) => l.id === mode.id);
  }, [mode, listData]);

  const [name, setName] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [metric, setMetric] = useState<LimiterMetric>("concurrency");
  const [capacity, setCapacity] = useState("10");
  const [windowMs, setWindowMs] = useState("60000");
  const [keyBy, setKeyBy] = useState<LimiterKeyBy>("shared");
  const [channelScope, setChannelScope] = useState<LimiterChannelScope>("admin");
  const [action, setAction] = useState<LimiterAction>("reject");
  const [queueSize, setQueueSize] = useState("0");
  const [queueTimeMs, setQueueTimeMs] = useState("0");
  const [priority, setPriority] = useState("0");

  // 只在首次拿到 existing 时回填一次，避免后台 refetch 覆盖正在编辑的输入。
  const prefilled = useRef(false);
  useEffect(() => {
    if (mode.kind === "edit" && existing && !prefilled.current) {
      prefilled.current = true;
      setName(existing.name);
      setEnabled(existing.enabled);
      setMetric(existing.metric);
      setCapacity(String(existing.capacity));
      setWindowMs(String(existing.window_ms || 60000));
      setKeyBy(existing.key_by);
      setChannelScope(existing.channel_scope || "admin");
      setAction(existing.action);
      setQueueSize(String(existing.queue_size));
      setQueueTimeMs(String(existing.queue_time_ms));
      setPriority(String(existing.priority));
    }
  }, [mode, existing]);

  // 条件渲染开关。
  const showWindow = metric === "rate";
  const showQueue = action === "wait";
  const showChannelScope = channelKeyed(keyBy);

  const pending = createMut.isPending || updateMut.isPending;

  // capacity 必须 >= 1：0 会拒绝一切请求，空输入更不能静默当 0 提交。
  const capacityNum = Number(capacity);
  const capacityValid = Number.isFinite(capacityNum) && capacityNum >= 1;

  const submit = async () => {
    if (!capacityValid) {
      toast.error(t("capacityRangeError"));
      return;
    }
    // 仅提交对当前维度有意义的字段，避免把隐藏字段的陈旧值写回。
    const body: Partial<RequestLimiter> = {
      name: name.trim(),
      enabled,
      metric,
      capacity: capacityNum,
      window_ms: showWindow ? Number(windowMs) || 0 : 0,
      key_by: keyBy,
      channel_scope: showChannelScope ? channelScope : "admin",
      action,
      queue_size: showQueue ? Number(queueSize) || 0 : 0,
      queue_time_ms: showQueue ? Number(queueTimeMs) || 0 : 0,
      priority: Number(priority) || 0,
    };
    try {
      if (mode.kind === "edit") {
        await updateMut.mutateAsync({ id: mode.id, ...body });
      } else {
        await createMut.mutateAsync(body);
      }
      toast.success(t("saved"));
      router.push("/rate-limiters");
    } catch (e) {
      toast.error(formatErrorToast(e, tc("save")));
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-lg border bg-card p-4 shadow-sm">
        <div className="flex items-center gap-2">
          <span className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
            {t("sectionPolicy")}
          </span>
          <span className="h-px flex-1 bg-border" />
        </div>

        <div className="mt-4 grid gap-4 md:grid-cols-2">
          {/* 名称 */}
          <div className="grid gap-1.5 md:col-span-2">
            <FieldLabel>{t("name")}</FieldLabel>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("namePlaceholder")}
            />
          </div>

          {/* 限什么 metric */}
          <div className="grid gap-1.5">
            <FieldLabel>{t("metric")}</FieldLabel>
            <Select
              value={metric}
              onValueChange={(v) => setMetric(v as LimiterMetric)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {METRICS.map((m) => (
                  <SelectItem key={m} value={m}>
                    {t(metricOptionKey(m))}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* 容量 capacity */}
          <div className="grid gap-1.5">
            <FieldLabel
              tip={
                metric === "rate"
                  ? t("capacityHintRate")
                  : t("capacityHintConcurrency")
              }
            >
              {t("capacity")}
            </FieldLabel>
            <Input
              type="number"
              min={1}
              value={capacity}
              onChange={(e) => setCapacity(e.target.value)}
              aria-invalid={!capacityValid}
              className="font-mono tabular-nums aria-[invalid=true]:border-destructive"
            />
            {!capacityValid ? (
              <p className="text-xs text-destructive">
                {t("capacityRangeError")}
              </p>
            ) : null}
          </div>

          {/* 时间窗口 window_ms —— 仅 metric=rate */}
          {showWindow ? (
            <div className="grid gap-1.5">
              <FieldLabel tip={t("windowMsHint")}>{t("windowMs")}</FieldLabel>
              <div className="relative">
                <Input
                  type="number"
                  min={0}
                  value={windowMs}
                  onChange={(e) => setWindowMs(e.target.value)}
                  className="pr-12 font-mono tabular-nums"
                />
                <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-muted-foreground">
                  {t("fieldMsSuffix")}
                </span>
              </div>
            </div>
          ) : null}

          {/* 额度怎么分 key_by */}
          <div className="grid gap-1.5">
            <FieldLabel>{t("keyBy")}</FieldLabel>
            <Select
              value={keyBy}
              onValueChange={(v) => setKeyBy(v as LimiterKeyBy)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {KEY_BYS.map((k) => (
                  <SelectItem key={k} value={k}>
                    {t(keyByOptionKey(k))}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* 作用渠道范围 channel_scope —— 仅 key_by ∈ {per_channel, per_channel_user}；
              其它 KeyBy 该字段无意义，置灰禁用 */}
          <div className="grid gap-1.5">
            <FieldLabel tip={t("channelScopeHint")}>
              {t("channelScope")}
            </FieldLabel>
            <Select
              value={channelScope}
              onValueChange={(v) => setChannelScope(v as LimiterChannelScope)}
              disabled={!showChannelScope}
            >
              <SelectTrigger className={showChannelScope ? "" : "opacity-50"}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHANNEL_SCOPES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {t(scopeOptionKey(s))}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* 超限动作 action */}
          <div className="grid gap-1.5">
            <FieldLabel>{t("action")}</FieldLabel>
            <Select
              value={action}
              onValueChange={(v) => setAction(v as LimiterAction)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ACTIONS.map((a) => (
                  <SelectItem key={a} value={a}>
                    {t(actionOptionKey(a))}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* 队列长度 / 最长排队时间 —— 仅 action=wait */}
          {showQueue ? (
            <>
              <div className="grid gap-1.5">
                <FieldLabel tip={t("queueSizeHint")}>
                  {t("queueSize")}
                </FieldLabel>
                <Input
                  type="number"
                  min={0}
                  value={queueSize}
                  onChange={(e) => setQueueSize(e.target.value)}
                  className="font-mono tabular-nums"
                />
              </div>
              <div className="grid gap-1.5">
                <FieldLabel tip={t("queueTimeMsHint")}>
                  {t("queueTimeMs")}
                </FieldLabel>
                <div className="relative">
                  <Input
                    type="number"
                    min={0}
                    value={queueTimeMs}
                    onChange={(e) => setQueueTimeMs(e.target.value)}
                    className="pr-12 font-mono tabular-nums"
                  />
                  <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-muted-foreground">
                    {t("fieldMsSuffix")}
                  </span>
                </div>
              </div>
            </>
          ) : null}

          {/* 优先级 priority */}
          <div className="grid gap-1.5">
            <FieldLabel tip={t("priorityHint")}>{t("priority")}</FieldLabel>
            <Input
              type="number"
              value={priority}
              onChange={(e) => setPriority(e.target.value)}
              className="font-mono tabular-nums"
            />
          </div>

          {/* 启用 enabled */}
          <div className="grid gap-1.5">
            <FieldLabel>{t("status")}</FieldLabel>
            <label className="flex h-9 cursor-pointer items-center gap-2 rounded-md border bg-background px-3">
              <Switch checked={enabled} onCheckedChange={setEnabled} />
              <span className="font-mono text-xs text-muted-foreground">
                {enabled ? tc("enabled") : tc("disabled")}
              </span>
            </label>
          </div>
        </div>
      </div>

      {/* 作用到谁：绑定管理。仅编辑态可用（需要已落库的 limiter id）。
          绑定校验在后端用的是 DB 里已落库的 key_by，这里就必须喂同一个口径
          （existing.key_by），否则下拉会把表单未保存的 key_by 当真，
          管理员选出来的目标类型提交时被后端用旧 key_by 拒掉。表单 keyBy 与
          已落库的 key_by 不一致时（policyDirty），让绑定区 disable 并提示先保存。 */}
      {mode.kind === "edit" ? (
        <BindingEditor
          limiterId={mode.id}
          keyBy={existing?.key_by ?? keyBy}
          policyDirty={existing != null && existing.key_by !== keyBy}
        />
      ) : (
        <div className="rounded-lg border border-dashed bg-muted/20 p-4">
          <p className="text-sm text-muted-foreground">
            {t("bindingsAfterSave")}
          </p>
        </div>
      )}

      {/* 操作 */}
      <div className="flex justify-end gap-2 border-t pt-4">
        <Button variant="outline" onClick={() => router.push("/rate-limiters")}>
          {tc("cancel")}
        </Button>
        <Button
          onClick={submit}
          disabled={pending || !name.trim() || !capacityValid}
        >
          {pending && <Loader2 className="mr-2 size-4 animate-spin" />}
          {tc("save")}
        </Button>
      </div>
    </div>
  );
}
