"use client";

import { UseFormReturn } from "react-hook-form";
import { useTranslations } from "next-intl";
import { Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from "@/components/ui/form";
import { Badge } from "@/components/ui/badge";
import {
  useRoutingCandidates,
  useRoutingCandidatesByToken,
} from "@/lib/api/model-routings";
import { RefCombobox } from "./ref-combobox";
import { RoutingFormValues } from "./routing-form/types";

export interface MembersRowEditorProps {
  form: UseFormReturn<RoutingFormValues>;
  index: number;
  onRemove: () => void;
  apiMode?: "admin" | "user";
  tokenKey?: string | null;
}

// 响应式行编辑器：
// - mobile (<sm)：外层 1 列 grid，ref 单独一行，priority/weight/delete 在
//   一个 sub-grid (1fr 1fr 40px) 排成第二行；inline label 让字段可识别
// - ≥sm：外层 4 列 grid `[1fr 88px 88px 40px]`，sub-grid 用 `sm:contents`
//   退化为透明容器，三个子元素直接落到 col 2/3/4，视觉上与改造前一致
export function MembersRowEditor({
  form,
  index,
  onRemove,
  apiMode = "admin",
  tokenKey,
}: MembersRowEditorProps) {
  const t = useTranslations("modelRoutings.members");
  const allMembers = form.watch("members");
  const alreadyAdded = (allMembers ?? []).map((m) => m.ref).filter(Boolean);
  const selfName = form.watch("name");

  const adminQuery = useRoutingCandidates({ enabled: apiMode === "admin" });
  const userQuery = useRoutingCandidatesByToken(
    apiMode === "user" ? tokenKey ?? null : null,
  );
  const candidates = apiMode === "user" ? userQuery.data : adminQuery.data;

  const refValue = form.watch(`members.${index}.ref` as const);
  const isStale =
    apiMode === "user" &&
    !!tokenKey &&
    !!refValue &&
    !!candidates &&
    ![...candidates.models, ...candidates.global_routings].includes(refValue);

  return (
    <div
      className={[
        // mobile：包成卡片，让相邻成员之间有明确边界（border + 轻底色 + 内边距）
        "rounded-lg border bg-muted/30 p-3",
        // 字段堆叠
        "grid grid-cols-1 gap-3",
        // ≥sm：撤掉卡片样式，回归原 4 列表格行
        "sm:rounded-none sm:border-0 sm:bg-transparent sm:p-0",
        "sm:grid-cols-[1fr_88px_88px_40px] sm:items-start sm:gap-2",
      ].join(" ")}
    >
      <FormField
        control={form.control}
        name={`members.${index}.ref` as const}
        render={({ field }) => (
          <FormItem className="min-w-0">
            <FormLabel className="text-xs text-muted-foreground sm:hidden">
              {t("refLabel")}
            </FormLabel>
            <FormControl>
              <div className="flex flex-col gap-1">
                <RefCombobox
                  value={field.value}
                  onChange={field.onChange}
                  alreadyAdded={alreadyAdded}
                  excludeSelf={selfName}
                  apiMode={apiMode}
                  tokenKey={tokenKey}
                />
                {isStale && (
                  <Badge variant="secondary" className="self-start text-xs">
                    {t("staleRef")}
                  </Badge>
                )}
              </div>
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />

      {/* sub-row：mobile 横向小行；≥sm 用 contents 让子元素回到外层 grid */}
      <div className="grid grid-cols-[1fr_1fr_40px] gap-2 sm:contents">
        <FormField
          control={form.control}
          name={`members.${index}.priority` as const}
          render={({ field }) => (
            <FormItem className="min-w-0">
              <FormLabel className="text-xs text-muted-foreground sm:hidden">
                {t("priorityLabel")}
              </FormLabel>
              <FormControl>
                <Input
                  type="number"
                  value={field.value}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                  min={0}
                  max={999}
                  className="text-center tabular-nums"
                />
              </FormControl>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name={`members.${index}.weight` as const}
          render={({ field }) => (
            <FormItem className="min-w-0">
              <FormLabel className="text-xs text-muted-foreground sm:hidden">
                {t("weightLabel")}
              </FormLabel>
              <FormControl>
                <Input
                  type="number"
                  value={field.value}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                  min={1}
                  max={999}
                  className="text-center tabular-nums"
                />
              </FormControl>
            </FormItem>
          )}
        />
        {/* mobile 下按钮垂直对齐到行底部（与 input 输入框对齐）；
            desktop 下保持现有外层 items-start 顶对齐 */}
        <div className="flex items-end sm:items-start sm:pt-0">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onRemove}
            aria-label={t("remove")}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}
