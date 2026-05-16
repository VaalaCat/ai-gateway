"use client";

import { Copy } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";

interface CopyableTextProps {
  text: string;
  display?: string;
  mono?: boolean;
}

export function CopyableText({ text, display, mono = true }: CopyableTextProps) {
  const t = useTranslations("common");
  const shown = display ?? text;
  return (
    <div className="flex items-center gap-1">
      <span className={`text-sm ${mono ? "font-mono" : ""}`}>{shown}</span>
      <Button
        variant="ghost"
        size="icon"
        className="size-6"
        onClick={() => {
          navigator.clipboard.writeText(text);
          toast.success(t("copied"));
        }}
      >
        <Copy className="size-3" />
      </Button>
    </div>
  );
}
