"use client";

import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

// 钩子作用域徽标：req = onRequest（入站）, up = onUpstreamRequest（出站）。
type Hook = "req" | "up";

interface ApiRow {
  sig: string;
  descKey: string;
  hooks: Hook[];
}

// ctx API 速查：与 spec 的能力表保持一致（docs/.../admin-dynamic-scripts-design.md）。
// 让管理员一眼看到可用方法——尤其是只在出站生效的 header 改写。
const ROWS: ApiRow[] = [
  { sig: "ctx.body", descKey: "apiBodyDesc", hooks: ["req", "up"] },
  { sig: "ctx.headers", descKey: "apiHeadersDesc", hooks: ["req", "up"] },
  { sig: "ctx.setHeader(name, value)", descKey: "apiSetHeaderDesc", hooks: ["up"] },
  { sig: "ctx.removeHeader(name)", descKey: "apiRemoveHeaderDesc", hooks: ["up"] },
  { sig: "ctx.reject(status, msg)", descKey: "apiRejectDesc", hooks: ["req", "up"] },
  { sig: "console.log(...)", descKey: "apiConsoleDesc", hooks: ["req", "up"] },
];

function HookBadge({ hook }: { hook: Hook }) {
  return (
    <span
      className={cn(
        "rounded border px-1 py-0.5 font-mono text-[10px] leading-none",
        hook === "up"
          ? "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400"
          : "border-border bg-muted/60 text-muted-foreground",
      )}
    >
      {hook === "up" ? "onUpstreamRequest" : "onRequest"}
    </span>
  );
}

export function ScriptApiReference() {
  const t = useTranslations("scripts");
  return (
    <div className="rounded-lg border bg-muted/20 p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
          {t("apiTitle")}
        </span>
        <span className="h-px flex-1 bg-border" />
      </div>
      <dl className="space-y-2.5">
        {ROWS.map((row) => (
          <div
            key={row.sig}
            className="flex flex-col gap-1 sm:flex-row sm:items-baseline sm:gap-3"
          >
            <dt className="shrink-0 font-mono text-xs text-foreground sm:w-56">{row.sig}</dt>
            <dd className="flex flex-1 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
              <span>{t(row.descKey)}</span>
              <span className="flex gap-1">
                {row.hooks.map((h) => (
                  <HookBadge key={h} hook={h} />
                ))}
              </span>
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
