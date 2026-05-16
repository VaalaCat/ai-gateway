"use client";

import { useState, useRef, useEffect } from "react";
import { Input } from "@/components/ui/input";

interface InlineEditProps {
  value: number;
  onSave: (value: number) => void;
  disabled?: boolean;
}

export function InlineEdit({ value, onSave, disabled }: InlineEditProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(String(value));
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [editing]);

  const save = () => {
    const num = Number(draft);
    if (!isNaN(num) && num !== value) {
      onSave(num);
    }
    setEditing(false);
  };

  if (editing) {
    return (
      <Input
        ref={inputRef}
        type="number"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === "Enter") save();
          if (e.key === "Escape") setEditing(false);
        }}
        className="h-7 w-16 text-center"
      />
    );
  }

  return (
    <button
      type="button"
      className="cursor-pointer rounded px-2 py-0.5 text-sm hover:bg-accent disabled:cursor-default disabled:opacity-50"
      onClick={() => {
        if (!disabled) {
          setDraft(String(value));
          setEditing(true);
        }
      }}
      disabled={disabled}
    >
      {value}
    </button>
  );
}
