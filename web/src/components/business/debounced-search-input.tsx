"use client";

import { useEffect, useRef, useState } from "react";

import { Input } from "@/components/ui/input";

interface DebouncedSearchInputProps {
  value: string;
  onCommit: (v: string) => void;
  placeholder: string;
  className?: string;
}

/**
 * 受控去抖搜索输入:本地即时回显,300ms 无新输入后才把值提交给调用方(通常写入 URL)。
 * 避免每次按键都触发一次网络请求/URL 替换。
 */
export function DebouncedSearchInput({
  value,
  onCommit,
  placeholder,
  className,
}: DebouncedSearchInputProps) {
  return (
    <DebouncedSearchDraft
      key={value}
      initialValue={value}
      onCommit={onCommit}
      placeholder={placeholder}
      className={className}
    />
  );
}

function DebouncedSearchDraft({
  initialValue,
  onCommit,
  placeholder,
  className,
}: Omit<DebouncedSearchInputProps, "value"> & { initialValue: string }) {
  const [local, setLocal] = useState(initialValue);
  const onCommitRef = useRef(onCommit);
  useEffect(() => {
    onCommitRef.current = onCommit;
  }, [onCommit]);

  useEffect(() => {
    const id = setTimeout(() => {
      if (local !== initialValue) onCommitRef.current(local);
    }, 300);
    return () => clearTimeout(id);
  }, [initialValue, local]);

  return (
    <Input
      value={local}
      onChange={(e) => setLocal(e.target.value)}
      placeholder={placeholder}
      className={className ?? "w-48"}
    />
  );
}
