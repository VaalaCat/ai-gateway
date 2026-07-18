"use client";

import { UseFormReturn, useFieldArray } from "react-hook-form";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { MembersRowEditor } from "../../members-row-editor";
import { RoutingFormValues } from "../types";

export interface MembersSectionProps {
  form: UseFormReturn<RoutingFormValues>;
  apiMode?: "admin" | "user";
  tokenKey?: string | null;
  limitCandidatesToToken?: boolean;
}

export function MembersSection({
  form,
  apiMode = "admin",
  tokenKey,
  limitCandidatesToToken = false,
}: MembersSectionProps) {
  const t = useTranslations("modelRoutings");

  const { fields, append, remove } = useFieldArray<RoutingFormValues>({
    control: form.control,
    name: "members",
  });

  function handleAdd() {
    if (fields.length >= 32) return;
    append({ ref: "", priority: 0, weight: 1 });
  }

  return (
    <section className="space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h3 className="text-base font-semibold">② {t("section.members")}</h3>
          <p className="text-sm text-muted-foreground">{t("section.membersDesc")}</p>
        </div>
        <span className="text-xs text-muted-foreground">
          {t("members.usage", { used: fields.length })}
        </span>
      </header>

      {/* 表头仅 ≥sm 显示；mobile 下堆叠行带 inline label 替代 */}
      {fields.length > 0 && (
        <div className="hidden sm:grid grid-cols-[1fr_88px_88px_40px] gap-2 px-1">
          <span className="text-xs font-medium text-muted-foreground">{t("members.refLabel")}</span>
          <span className="text-xs font-medium text-muted-foreground text-center">{t("members.priorityLabel")}</span>
          <span className="text-xs font-medium text-muted-foreground text-center">{t("members.weightLabel")}</span>
          <span />
        </div>
      )}

      {/* mobile space-y-3 让相邻卡片之间有 12px gap > 卡片内 gap-3 (12px)
          单看间距相等，但配合每张卡的 border + bg-muted/30 已能清晰分隔；
          desktop 退回 space-y-2 保持紧凑表格 */}
      <div className="space-y-3 sm:space-y-2">
        {fields.map((field, index) => (
          <MembersRowEditor
            key={field.id}
            form={form}
            index={index}
            onRemove={() => remove(index)}
            apiMode={apiMode}
            tokenKey={tokenKey}
            limitCandidatesToToken={limitCandidatesToToken}
          />
        ))}
      </div>

      {fields.length === 0 && (
        <div className="rounded-md border border-dashed px-4 py-6 text-center">
          <p className="text-sm font-medium text-muted-foreground">{t("members.empty")}</p>
          <p className="text-xs text-muted-foreground mt-1">{t("members.emptyHint")}</p>
        </div>
      )}

      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={handleAdd}
        disabled={fields.length >= 32}
      >
        {t("members.add")}
      </Button>
    </section>
  );
}
