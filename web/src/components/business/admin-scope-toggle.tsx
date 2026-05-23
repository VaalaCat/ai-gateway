"use client";

import { useTranslations } from "next-intl";
import { Globe, User } from "lucide-react";

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

export type AdminScope = "self" | "global";

interface AdminScopeToggleProps {
  value: AdminScope;
  onChange: (scope: AdminScope) => void;
  className?: string;
}

export function AdminScopeToggle({
  value,
  onChange,
  className,
}: AdminScopeToggleProps) {
  const t = useTranslations("common.adminScope");
  return (
    <Tabs
      value={value}
      onValueChange={(v) => onChange(v as AdminScope)}
      className={cn("w-full", className)}
    >
      <TabsList className="grid w-full grid-cols-2">
        <TabsTrigger value="self" className="gap-1.5">
          <User className="size-3.5" aria-hidden />
          {t("self")}
        </TabsTrigger>
        <TabsTrigger value="global" className="gap-1.5">
          <Globe className="size-3.5" aria-hidden />
          {t("global")}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  );
}
