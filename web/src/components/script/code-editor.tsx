"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { codeToHtml } from "shiki";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

const MonacoEditor = dynamic(() => import("@monaco-editor/react"), { ssr: false });

const MONO_STACK =
  "var(--font-geist-mono), var(--font-datatype), ui-monospace, SFMono-Regular, monospace";

interface Props {
  value: string;
  onChange: (v: string) => void;
  filename?: string;
}

// CodeEditor 把脚本当成一份代码制品来呈现：编辑器 chrome（文件名 tab + 语言徽标 +
// 行数）包裹两种编辑形态——
//  - 富编辑器：Monaco（功能全，体积大，ssr:false）
//  - 轻量高亮：透明 textarea 叠在 Shiki 生成的高亮层上（mono 字体，移动端更轻）
export function CodeEditor({ value, onChange, filename }: Props) {
  const t = useTranslations("scripts");
  const [rich, setRich] = useState(true);
  const [html, setHtml] = useState("");
  const hlRef = useRef<HTMLDivElement>(null);

  const lines = useMemo(() => value.split("\n").length, [value]);
  const file = `${(filename || "script").trim() || "script"}.js`;

  useEffect(() => {
    if (rich) return;
    let alive = true;
    codeToHtml(value || " ", { lang: "javascript", theme: "github-dark" })
      .then((h) => {
        if (alive) setHtml(h);
      })
      .catch(() => {
        if (alive) setHtml("");
      });
    return () => {
      alive = false;
    };
  }, [value, rich]);

  return (
    <div className="overflow-hidden rounded-lg border bg-[#0d1117] shadow-sm">
      {/* tab / chrome 栏 */}
      <div className="flex items-center gap-3 border-b border-white/10 bg-[#161b22] px-3 py-2">
        <div className="flex items-center gap-1.5 rounded-md border border-white/10 bg-white/5 px-2.5 py-1">
          <span className="text-[11px] font-semibold text-amber-300/90">JS</span>
          <span className="font-mono text-xs text-zinc-200">{file}</span>
        </div>
        <span className="hidden font-mono text-[11px] text-zinc-500 sm:inline">javascript</span>

        <div className="ml-auto flex items-center overflow-hidden rounded-md border border-white/10">
          <ModeButton active={rich} onClick={() => setRich(true)}>
            {t("editorRich")}
          </ModeButton>
          <ModeButton active={!rich} onClick={() => setRich(false)}>
            {t("editorLight")}
          </ModeButton>
        </div>
      </div>

      {/* 编辑区 */}
      {rich ? (
        <MonacoEditor
          height="380px"
          defaultLanguage="javascript"
          theme="vs-dark"
          value={value}
          onChange={(v) => onChange(v ?? "")}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            fontFamily: MONO_STACK,
            fontLigatures: true,
            padding: { top: 14, bottom: 14 },
            scrollBeyondLastLine: false,
            renderLineHighlight: "none",
            smoothScrolling: true,
            cursorBlinking: "smooth",
            lineNumbersMinChars: 3,
          }}
        />
      ) : (
        <div className="relative h-[380px] overflow-hidden bg-[#0d1117]">
          <div
            ref={hlRef}
            aria-hidden
            className="pointer-events-none absolute inset-0 overflow-auto p-3.5 font-mono text-[13px] leading-[1.55] [&_pre]:!m-0 [&_pre]:!bg-transparent"
            dangerouslySetInnerHTML={{ __html: html }}
          />
          <textarea
            value={value}
            spellCheck={false}
            onChange={(e) => onChange(e.target.value)}
            onScroll={(e) => {
              if (hlRef.current) {
                hlRef.current.scrollTop = e.currentTarget.scrollTop;
                hlRef.current.scrollLeft = e.currentTarget.scrollLeft;
              }
            }}
            className="relative block h-full w-full resize-none overflow-auto whitespace-pre bg-transparent p-3.5 font-mono text-[13px] leading-[1.55] text-transparent caret-white outline-none"
          />
        </div>
      )}

      {/* 状态栏 */}
      <div className="flex items-center gap-4 border-t border-white/10 bg-[#161b22] px-3 py-1.5 font-mono text-[11px] text-zinc-500">
        <span>UTF-8</span>
        <span>LF</span>
        <span className="ml-auto tabular-nums">
          {lines} {t("linesLabel")}
        </span>
      </div>
    </div>
  );
}

function ModeButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "px-2.5 py-1 font-mono text-[11px] transition-colors",
        active
          ? "bg-white/15 text-zinc-100"
          : "text-zinc-500 hover:bg-white/5 hover:text-zinc-300",
      )}
    >
      {children}
    </button>
  );
}
