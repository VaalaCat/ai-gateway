"use client";

import { useSyncExternalStore } from "react";

export type Breakpoint = "xs" | "sm-md" | "lg+";

const SM_QUERY = "(min-width: 640px)";
const LG_QUERY = "(min-width: 1024px)";

// 模块级 lazy-init,让 subscribe 和 getSnapshot 共享同一 mql 实例,
// 避免"两处独立 window.matchMedia 调用 + 依赖浏览器缓存才相等"的隐性耦合.
let smMql: MediaQueryList | null = null;
let lgMql: MediaQueryList | null = null;

function ensureMqls(): { sm: MediaQueryList; lg: MediaQueryList } | null {
  if (typeof window === "undefined") return null;
  smMql ??= window.matchMedia(SM_QUERY);
  lgMql ??= window.matchMedia(LG_QUERY);
  return { sm: smMql, lg: lgMql };
}

function getSnapshot(): Breakpoint {
  // useSyncExternalStore 在 SSR 走 getServerSnapshot,这里只在 client 被调.
  const mqls = ensureMqls();
  if (!mqls) return "lg+";
  if (mqls.lg.matches) return "lg+";
  if (mqls.sm.matches) return "sm-md";
  return "xs";
}

function getServerSnapshot(): Breakpoint {
  return "lg+";
}

function subscribe(onChange: () => void): () => void {
  const mqls = ensureMqls();
  if (!mqls) return () => {};
  mqls.sm.addEventListener("change", onChange);
  mqls.lg.addEventListener("change", onChange);
  return () => {
    mqls.sm.removeEventListener("change", onChange);
    mqls.lg.removeEventListener("change", onChange);
  };
}

/**
 * 返回当前 tailwind 风格断点(三档):
 *   - 'xs'    : < 640px
 *   - 'sm-md' : 640-1023px
 *   - 'lg+'   : >= 1024px
 *
 * SSR 安全:服务端默认 'lg+',客户端 hydration 后由 useSyncExternalStore
 * 切到实际断点;subscribe 监听 matchMedia change 自动更新.
 * 内部 mql 实例懒初始化共享,subscribe 和 getSnapshot 读同一对象.
 */
export function useBreakpoint(): Breakpoint {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
