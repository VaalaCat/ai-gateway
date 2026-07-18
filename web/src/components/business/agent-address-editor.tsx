"use client";

import { useState, useCallback } from "react";
import { useTranslations } from "next-intl";
import { Plus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface AgentAddress {
  url: string;
  tag: string;
}

interface AgentAddressEditorProps {
  value: string;
  onChange: (value: string) => void;
}

function parseAddresses(raw: string): AgentAddress[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed;
  } catch {
    // ignore
  }
  return [];
}

export function AgentAddressEditor({ value, onChange }: AgentAddressEditorProps) {
  const t = useTranslations("agents");
  const [draft, setDraft] = useState(() => ({ baseline: value, addresses: parseAddresses(value) }));
  const addresses = draft.baseline === value ? draft.addresses : parseAddresses(value);

  const emit = useCallback(
    (next: AgentAddress[]) => {
      setDraft({ baseline: value, addresses: next });
      onChange(next.length > 0 ? JSON.stringify(next) : "");
    },
    [onChange, value]
  );

  const addAddress = () => {
    emit([...addresses, { url: "", tag: "" }]);
  };

  const removeAddress = (index: number) => {
    emit(addresses.filter((_, i) => i !== index));
  };

  const updateAddress = (index: number, field: keyof AgentAddress, val: string) => {
    const next = addresses.map((a, i) =>
      i === index ? { ...a, [field]: val } : a
    );
    emit(next);
  };

  return (
    <div className="space-y-2">
      <Label>{t("httpAddresses")}</Label>
      {addresses.map((addr, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            className="flex-1"
            placeholder={t("addressUrlPlaceholder")}
            value={addr.url}
            onChange={(e) => updateAddress(i, "url", e.target.value)}
          />
          <Input
            className="w-32"
            placeholder={t("addressTagPlaceholder")}
            value={addr.tag}
            onChange={(e) => updateAddress(i, "tag", e.target.value)}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="shrink-0"
            onClick={() => removeAddress(i)}
          >
            <X className="size-4" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={addAddress}>
        <Plus className="mr-1 size-4" />
        {t("addAddress")}
      </Button>
    </div>
  );
}
