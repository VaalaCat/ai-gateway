"use client";

import { UseFormReturn } from "react-hook-form";
import { useTranslations } from "next-intl";
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { SearchableSelect } from "@/components/ui/searchable-select";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { RoutingFormValues } from "../types";

export interface BasicSectionProps {
  form: UseFormReturn<RoutingFormValues>;
  apiMode?: "admin" | "user";
  // 仅 user 模式生效——由 RoutingForm 透传：
  selectedTokenID?: string;
  onSelectTokenID?: (id: string) => void;
  tokenOptions?: { id: string; name: string }[];
}

export function BasicSection({
  form,
  apiMode = "admin",
  selectedTokenID,
  onSelectTokenID,
  tokenOptions,
}: BasicSectionProps) {
  const t = useTranslations("modelRoutings");
  const isUserMode = apiMode === "user";
  const scope = form.watch("scope");

  return (
    <section className="space-y-4">
      <header>
        <h3 className="text-base font-semibold">① {t("section.basic")}</h3>
        <p className="text-sm text-muted-foreground">{t("section.basicDesc")}</p>
      </header>

      <FormField
        control={form.control}
        name="name"
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t("field.name")}</FormLabel>
            <FormControl>
              <Input {...field} placeholder={t("field.namePlaceholder")} />
            </FormControl>
            <p className="text-xs text-muted-foreground">{t("field.nameHint")}</p>
            <FormMessage />
          </FormItem>
        )}
      />

      {apiMode === "user" && (
        <FormItem>
          <FormLabel>{t("field.token")}</FormLabel>
          <SearchableSelect
            value={selectedTokenID ?? ""}
            onChange={(v) => onSelectTokenID?.(v)}
            placeholder={t("field.token")}
            searchPlaceholder={t("field.token")}
            items={(tokenOptions ?? []).map((opt) => ({
              value: opt.id,
              label: opt.name || `Token #${opt.id}`,
            }))}
          />
          <p className="text-xs text-muted-foreground">{t("field.tokenHint")}</p>
        </FormItem>
      )}

      {!isUserMode && (
        <FormField
          control={form.control}
          name="scope"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t("scope.label")}</FormLabel>
              <RadioGroup
                value={field.value}
                onValueChange={field.onChange}
                className="flex gap-4"
              >
                <div className="flex items-center gap-2">
                  <RadioGroupItem value="global" id="scope-global" />
                  <label htmlFor="scope-global">{t("scope.global")}</label>
                </div>
                <div className="flex items-center gap-2">
                  <RadioGroupItem value="user" id="scope-user" />
                  <label htmlFor="scope-user">{t("scope.user")}</label>
                </div>
              </RadioGroup>
            </FormItem>
          )}
        />
      )}

      {scope === "user" && !isUserMode && (
        <FormField
          control={form.control}
          name="user_id"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t("field.userId")}</FormLabel>
              <FormControl>
                <EntityPicker
                  entity="user"
                  value={field.value ? String(field.value) : ""}
                  onChange={(v) => field.onChange(v ? Number(v) : 0)}
                  className="w-full"
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      )}

      <FormField
        control={form.control}
        name="enabled"
        render={({ field }) => (
          <FormItem className="flex items-center justify-between">
            <div>
              <FormLabel>{t("field.enabled")}</FormLabel>
              <p className="text-xs text-muted-foreground">
                {isUserMode ? t("field.enabledHintUser") : t("field.enabledHint")}
              </p>
            </div>
            <Switch checked={field.value} onCheckedChange={field.onChange} />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name="remark"
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t("field.remark")}</FormLabel>
            <FormControl>
              <Textarea {...field} rows={2} />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
    </section>
  );
}
