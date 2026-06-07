"use client";

import { useState, KeyboardEvent } from "react";
import { X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { useTranslations } from "next-intl";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

interface TagInputProps {
  value: string[];
  onChange: (tags: string[]) => void;
  placeholder?: string;
}

export function TagInput({ value, onChange, placeholder }: TagInputProps) {
  const [input, setInput] = useState("");
  const tc = useTranslations("common");

  const addTags = (text: string) => {
    const newTags = text.split(",").map(t => t.trim()).filter(t => t && !value.includes(t));
    if (newTags.length > 0) {
      onChange([...value, ...newTags]);
    }
    setInput("");
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if ((e.key === "Enter" || e.key === "Tab") && input.trim()) {
      e.preventDefault();
      addTags(input);
    }
    // Backspace on empty input removes last tag
    if (e.key === "Backspace" && !input && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  const handlePaste = (e: React.ClipboardEvent) => {
    const text = e.clipboardData.getData("text");
    if (text.includes(",")) {
      e.preventDefault();
      addTags(text);
    }
  };

  const removeTag = (index: number) => {
    onChange(value.filter((_, i) => i !== index));
  };

  const copyTag = (tag: string) => {
    copyTextWithFeedback(tag, { success: tc("copied"), error: tc("copyFailed") });
  };

  return (
    <div className="flex flex-wrap gap-2 rounded-md border p-2 focus-within:ring-2 focus-within:ring-ring">
      {value.map((tag, i) => (
        <Badge key={i} variant="secondary" className="gap-1 pr-1">
          <span
            className="cursor-pointer hover:underline"
            onClick={() => copyTag(tag)}
            title={tc("copy")}
          >
            {tag}
          </span>
          <button
            type="button"
            onClick={() => removeTag(i)}
            className="ml-1 rounded-full hover:bg-muted-foreground/20 p-0.5"
          >
            <X className="size-3" />
          </button>
        </Badge>
      ))}
      <Input
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        onBlur={() => { if (input.trim()) addTags(input); }}
        placeholder={value.length === 0 ? placeholder : ""}
        className="flex-1 min-w-[120px] border-0 p-0 shadow-none focus-visible:ring-0"
      />
    </div>
  );
}
