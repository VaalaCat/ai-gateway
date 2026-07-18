"use client";

import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2, Plus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import { EntityLabel } from "@/components/business/entity-label";
import type { EntityName } from "@/components/business/entity-picker/registry";
import { FieldTip } from "@/components/business/field-tip";

import {
  useLimiterBindings,
  useCreateLimiterBinding,
  useDeleteLimiterBinding,
} from "@/lib/api/rate-limiters";
import { formatErrorToast } from "@/lib/api/error-toast";
import type {
  LimiterBinding,
  LimiterKeyBy,
  LimiterTargetType,
} from "@/lib/types";

const ALL_TARGET_TYPES: LimiterTargetType[] = [
  "global",
  "channel",
  "user_group",
  "user",
];

// validBindingTarget 是后端 models.ValidBindingTarget 的 TS 镜像（§5.1）：
// KeyBy 决定一条 limiter 能绑哪类目标，二者必须保持一致，否则后端会 400。
export function validBindingTarget(
  keyBy: LimiterKeyBy,
  targetType: LimiterTargetType,
): boolean {
  switch (keyBy) {
    case "shared":
      return targetType === "global";
    case "per_user":
      return (
        targetType === "global" ||
        targetType === "user_group" ||
        targetType === "user"
      );
    case "per_group":
      return targetType === "global" || targetType === "user_group";
    case "per_channel":
    case "per_channel_user":
      return targetType === "global" || targetType === "channel";
    default:
      return false;
  }
}

function targetTypeOptionKey(
  t: LimiterTargetType,
):
  | "targetGlobal"
  | "targetChannel"
  | "targetUserGroup"
  | "targetUser" {
  switch (t) {
    case "channel":
      return "targetChannel";
    case "user_group":
      return "targetUserGroup";
    case "user":
      return "targetUser";
    default:
      return "targetGlobal";
  }
}

// 非全局 target_type 映射到 EntityPicker / EntityLabel 的 adapter 名（注意下划线↔连字符）。
function entityNameOf(targetType: LimiterTargetType): EntityName | null {
  switch (targetType) {
    case "channel":
      return "channel";
    case "user_group":
      return "user-group";
    case "user":
      return "user";
    default:
      return null;
  }
}

// 一条已存在绑定的标签：global 直接显示文案，其余用 EntityLabel 解析对象名。
function BindingTag({
  binding,
  onRemove,
  removing,
}: {
  binding: LimiterBinding;
  onRemove: () => void;
  removing: boolean;
}) {
  const t = useTranslations("rateLimiters");
  const entity = entityNameOf(binding.target_type);

  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border bg-muted/40 py-1 pl-2.5 pr-1 text-sm">
      <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        {t(targetTypeOptionKey(binding.target_type))}
      </span>
      <span className="font-medium">
        {entity ? (
          <EntityLabel entity={entity} id={binding.target_id} />
        ) : (
          t("targetGlobalLabel")
        )}
      </span>
      {/* shadcn Button 内 svg 会 pointer-events-none，删除按钮做成独立元素而非 Button */}
      <button
        type="button"
        aria-label={t("removeBinding")}
        onClick={onRemove}
        disabled={removing}
        className="flex size-5 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
      >
        {removing ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <X className="size-3.5" />
        )}
      </button>
    </span>
  );
}

export function BindingEditor({
  limiterId,
  keyBy,
  policyDirty = false,
}: {
  limiterId: number;
  // keyBy 必须是已落库的口径（后端 CreateBinding 重新从 DB 读 limiter.KeyBy 校验），
  // 不能传表单里未保存的实时值，否则下拉给出的目标类型会与后端校验对不上。
  keyBy: LimiterKeyBy;
  // 表单里的 key_by 被改且尚未保存时为 true：此时禁用加绑定，提示先保存策略。
  policyDirty?: boolean;
}) {
  const t = useTranslations("rateLimiters");
  const tc = useTranslations("common");

  const qc = useQueryClient();
  const { data: bindings, isLoading } = useLimiterBindings(limiterId);
  const createMut = useCreateLimiterBinding();
  const deleteMut = useDeleteLimiterBinding();

  // 当前 key_by 允许的 target_type（即时校验：非法组合根本不出现在下拉里）。
  const allowedTypes = useMemo(
    () => ALL_TARGET_TYPES.filter((tt) => validBindingTarget(keyBy, tt)),
    [keyBy],
  );

  const [targetType, setTargetType] = useState<LimiterTargetType>(
    allowedTypes[0] ?? "global",
  );
  const [targetSelection, setTargetSelection] = useState<{ type: LimiterTargetType; values: string[] }>({
    type: allowedTypes[0] ?? "global",
    values: [],
  });
  const [isBatching, setIsBatching] = useState(false);
  const [deletingId, setDeletingId] = useState<number | null>(null);

  // key_by 改了导致当前选择不再合法时，回退到第一个合法项。
  const effectiveType = validBindingTarget(keyBy, targetType)
    ? targetType
    : allowedTypes[0] ?? "global";

  const targetValues = targetSelection.type === effectiveType ? targetSelection.values : [];
  const setTargetValues = (values: string[]) => setTargetSelection({ type: effectiveType, values });

  const entity = entityNameOf(effectiveType);
  const needsObject = entity !== null;

  // 当前类型下已绑定的 target_id（多选里排除，避免撞 uk_limiter_binding 唯一约束 → 409）
  const boundIdsOfType = (bindings ?? [])
    .filter((b) => b.target_type === effectiveType)
    .map((b) => String(b.target_id));

  const onTypeChange = (v: string) => {
    setTargetType(v as LimiterTargetType);
    setTargetValues([]);
  };

  const handleAdd = async () => {
    if (policyDirty) {
      toast.error(t("bindingPolicyDirty"));
      return;
    }
    if (!validBindingTarget(keyBy, effectiveType)) {
      toast.error(t("bindingInvalidCombo"));
      return;
    }
    // global：无对象，单条绑定
    if (!needsObject) {
      try {
        await createMut.mutateAsync({
          limiter_id: limiterId,
          target_type: effectiveType,
          target_id: 0,
        });
        toast.success(t("bindingAdded"));
      } catch (e) {
        toast.error(formatErrorToast(e, t("addBinding")));
      }
      return;
    }
    if (targetValues.length === 0) {
      toast.error(t("bindingPickObject"));
      return;
    }
    // 对象类型：客户端循环批量建，结束后统一 invalidate 一次。
    // createMut.isPending 只反映最后一次 mutation，批量中途不可靠，用 isBatching 兜底。
    setIsBatching(true);
    try {
      const results = await Promise.allSettled(
        targetValues.map((v) =>
          createMut.mutateAsync({
            limiter_id: limiterId,
            target_type: effectiveType,
            target_id: Number(v) || 0,
          }),
        ),
      );
      qc.invalidateQueries({ queryKey: ["limiter-bindings", limiterId] });
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const fail = results.length - ok;
      if (ok > 0) {
        toast.success(t("bindingAddedN", { count: ok }));
        setTargetValues([]);
      }
      if (fail > 0) {
        toast.error(t("bindingAddFailedN", { count: fail }));
      }
    } finally {
      setIsBatching(false);
    }
  };

  const handleRemove = async (b: LimiterBinding) => {
    setDeletingId(b.id);
    try {
      await deleteMut.mutateAsync({ id: b.id, limiterId });
      toast.success(t("bindingRemoved"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("delete")));
    } finally {
      setDeletingId(null);
    }
  };

  const list = bindings ?? [];

  return (
    <div className="rounded-lg border bg-card p-4 shadow-sm">
      <div className="flex items-center gap-2">
        <span className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
          {t("sectionBindings")}
        </span>
        <FieldTip text={t("sectionBindingsHint")} />
        <span className="h-px flex-1 bg-border" />
      </div>

      {/* 已有绑定列表 */}
      <div className="mt-4">
        {isLoading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {tc("loading")}
          </div>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("bindingEmpty")}</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {list.map((b) => (
              <BindingTag
                key={b.id}
                binding={b}
                removing={deletingId === b.id}
                onRemove={() => handleRemove(b)}
              />
            ))}
          </div>
        )}
      </div>

      {/* key_by 改了还没保存：绑定区与后端校验口径会打架，先提示+禁用。 */}
      {policyDirty ? (
        <p className="mt-4 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
          {t("bindingPolicyDirty")}
        </p>
      ) : null}

      {/* 加绑定 */}
      <div className="mt-4 flex flex-col gap-2 border-t pt-4 sm:flex-row sm:items-end">
        <div className="grid gap-1.5 sm:w-44">
          <Label className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
            {t("bindingTargetType")}
          </Label>
          <Select
            value={effectiveType}
            onValueChange={onTypeChange}
            disabled={policyDirty}
          >
            <SelectTrigger className={policyDirty ? "opacity-50" : ""}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {allowedTypes.map((tt) => (
                <SelectItem key={tt} value={tt}>
                  {t(targetTypeOptionKey(tt))}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {needsObject && entity ? (
          <div className="grid flex-1 gap-1.5">
            <Label className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
              {t("bindingTargetObject")}
            </Label>
            <EntityMultiPicker
              entity={entity}
              value={targetValues}
              onChange={setTargetValues}
              excludeIds={boundIdsOfType}
              disabled={policyDirty}
            />
          </div>
        ) : null}

        <Button
          onClick={handleAdd}
          disabled={
            policyDirty ||
            isBatching ||
            createMut.isPending ||
            (needsObject && targetValues.length === 0)
          }
          className="sm:w-auto"
        >
          {isBatching || createMut.isPending ? (
            <Loader2 className="mr-2 size-4 animate-spin" />
          ) : (
            <Plus className="mr-2 size-4" />
          )}
          {t("addBinding")}
        </Button>
      </div>
    </div>
  );
}
