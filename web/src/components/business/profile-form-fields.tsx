"use client";

import { useTranslations } from "next-intl";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export interface ProfileFormValue {
  email: string;
  display_name: string;
  avatar_url: string;
}

export interface ProfileFormFieldsProps {
  value: ProfileFormValue;
  onChange: (next: ProfileFormValue) => void;
  /** 头像首字母 fallback 的来源,通常传 username */
  fallbackInitial?: string;
  /** Email 字段下方的红字错误,通常来自 server "email_taken" */
  emailError?: string;
  /** Avatar 字段下方的红字错误,通常来自客户端校验失败 */
  avatarError?: string;
  /** 提交中时父级传 true,disable 所有输入 */
  disabled?: boolean;
  /** 输入元素 id 前缀,避免一个页面同时挂多份(self+admin 时)冲突 */
  idPrefix?: string;
}

const MAX = {
  email: 191,
  display_name: 64,
  avatar_url: 512,
} as const;

function pickInitial(displayName: string, fallback: string | undefined): string {
  const src = displayName.trim() || fallback?.trim() || "U";
  return src.charAt(0).toUpperCase();
}

export function ProfileFormFields({
  value,
  onChange,
  fallbackInitial,
  emailError,
  avatarError,
  disabled,
  idPrefix = "pf",
}: ProfileFormFieldsProps) {
  const t = useTranslations("profile.form");

  const update = <K extends keyof ProfileFormValue>(key: K, v: string) =>
    onChange({ ...value, [key]: v });

  const initial = pickInitial(value.display_name, fallbackInitial);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-4">
        <Avatar size="lg">
          {value.avatar_url && <AvatarImage src={value.avatar_url} alt={value.display_name || fallbackInitial || ""} />}
          <AvatarFallback>{initial}</AvatarFallback>
        </Avatar>
        <div className="flex-1 space-y-1.5">
          <Label htmlFor={`${idPrefix}-avatar`}>{t("avatarUrl")}</Label>
          <Input
            id={`${idPrefix}-avatar`}
            type="url"
            placeholder="https://..."
            value={value.avatar_url}
            onChange={(e) => update("avatar_url", e.target.value)}
            maxLength={MAX.avatar_url}
            disabled={disabled}
          />
          {avatarError ? (
            <p className="text-xs text-destructive">{avatarError}</p>
          ) : (
            <p className="text-xs text-muted-foreground">{t("avatarHint")}</p>
          )}
        </div>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-display-name`}>{t("displayName")}</Label>
        <Input
          id={`${idPrefix}-display-name`}
          value={value.display_name}
          onChange={(e) => update("display_name", e.target.value)}
          maxLength={MAX.display_name}
          disabled={disabled}
        />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-email`}>{t("email")}</Label>
        <Input
          id={`${idPrefix}-email`}
          type="email"
          value={value.email}
          onChange={(e) => update("email", e.target.value)}
          maxLength={MAX.email}
          disabled={disabled}
        />
        {emailError && <p className="text-xs text-destructive">{emailError}</p>}
      </div>
    </div>
  );
}
