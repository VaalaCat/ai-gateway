"use client";

import { useTranslations } from "next-intl";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface StatusSelectProps {
  value: string;
  onChange: (value: string) => void;
  showLabel?: boolean;
  disabled?: boolean;
}

export function StatusSelect({ value, onChange, showLabel = true, disabled }: StatusSelectProps) {
  const t = useTranslations("common");
  return (
    <div className="space-y-2">
      {showLabel && <Label>{t("status")}</Label>}
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="1">{t("enabled")}</SelectItem>
          <SelectItem value="0">{t("disabled")}</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}
