"use client";

import { Copy } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

interface CopyableTextProps {
  text: string;
  display?: string;
  mono?: boolean;
  /**
   * When true, the (possibly truncated) display becomes a click target that
   * opens a popover showing the full value plus its own copy button. Leave
   * false for formatted-short displays like `#123` where revealing adds nothing.
   */
  revealable?: boolean;
}

export function CopyableText({
  text,
  display,
  mono = true,
  revealable = false,
}: CopyableTextProps) {
  const t = useTranslations("common");
  const shown = display ?? text;
  const monoCls = mono ? "font-mono" : "";

  const copy = () =>
    copyTextWithFeedback(text, { success: t("copied"), error: t("copyFailed") });

  const label = revealable ? (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("showFull")}
          className={`text-sm ${monoCls} cursor-pointer underline-offset-2 hover:underline`}
        >
          {shown}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto max-w-sm">
        <div className="flex items-start gap-2">
          <code className="flex-1 select-all break-all font-mono text-xs">{text}</code>
          <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={copy}>
            <Copy className="size-3" />
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  ) : (
    <span className={`text-sm ${monoCls}`}>{shown}</span>
  );

  return (
    <div className="flex items-center gap-1">
      {label}
      <Button variant="ghost" size="icon" className="size-6" onClick={copy}>
        <Copy className="size-3" />
      </Button>
    </div>
  );
}
