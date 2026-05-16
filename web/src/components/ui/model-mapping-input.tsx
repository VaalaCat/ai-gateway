"use client";

import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useTranslations } from "next-intl";

interface ModelMappingInputProps {
  value: string; // JSON string: {"source": "target", ...}
  onChange: (json: string) => void;
  onMappingAdd?: (sourceModel: string) => void;
  onMappingRemove?: (sourceModel: string) => void;
}

interface MappingEntry {
  source: string;
  target: string;
}

function parseMapping(json: string): MappingEntry[] {
  try {
    const obj = JSON.parse(json);
    return Object.entries(obj).map(([source, target]) => ({ source, target: target as string }));
  } catch {
    return [];
  }
}

function serializeMapping(entries: MappingEntry[]): string {
  const obj: Record<string, string> = {};
  for (const e of entries) {
    if (e.source.trim()) {
      obj[e.source.trim()] = e.target.trim();
    }
  }
  return Object.keys(obj).length > 0 ? JSON.stringify(obj) : "";
}

export function ModelMappingInput({ value, onChange, onMappingAdd, onMappingRemove }: ModelMappingInputProps) {
  const tc = useTranslations("common");
  const [entries, setEntries] = useState<MappingEntry[]>(() => parseMapping(value));

  const updateEntries = (newEntries: MappingEntry[]) => {
    setEntries(newEntries);
    onChange(serializeMapping(newEntries));
  };

  const addEntry = () => {
    updateEntries([...entries, { source: "", target: "" }]);
  };

  const removeEntry = (index: number) => {
    const entry = entries[index];
    if (entry.source.trim()) {
      onMappingRemove?.(entry.source.trim());
    }
    updateEntries(entries.filter((_, i) => i !== index));
  };

  const updateEntry = (index: number, field: "source" | "target", val: string) => {
    if (field === "source") {
      const oldValue = entries[index].source.trim();
      const newValue = val.trim();
      if (oldValue && newValue && oldValue !== newValue) {
        onMappingRemove?.(oldValue);
        onMappingAdd?.(newValue);
      } else if (!oldValue && newValue) {
        onMappingAdd?.(newValue);
      } else if (oldValue && !newValue) {
        onMappingRemove?.(oldValue);
      }
    }
    const newEntries = entries.map((e, i) => i === index ? { ...e, [field]: val } : e);
    updateEntries(newEntries);
  };

  return (
    <div className="space-y-2">
      {entries.map((entry, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={entry.source}
            onChange={(e) => updateEntry(i, "source", e.target.value)}
            placeholder="source model"
            className="flex-1"
          />
          <span className="text-muted-foreground shrink-0">→</span>
          <Input
            value={entry.target}
            onChange={(e) => updateEntry(i, "target", e.target.value)}
            placeholder="target model"
            className="flex-1"
          />
          <Button type="button" variant="ghost" size="icon" onClick={() => removeEntry(i)} className="shrink-0">
            <Trash2 className="size-4" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={addEntry}>
        <Plus className="mr-1 size-4" />
        {tc("create")}
      </Button>
    </div>
  );
}
