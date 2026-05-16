"use client";

import { useState, useCallback } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

interface JsonFieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  tip?: React.ReactNode;
}

export function JsonField({ label, value, onChange, placeholder, rows = 3, tip }: JsonFieldProps) {
  const [error, setError] = useState<string | null>(null);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const v = e.target.value;
      onChange(v);
      if (v.trim() === "") {
        setError(null);
      } else {
        try {
          JSON.parse(v);
          setError(null);
        } catch {
          setError("Invalid JSON");
        }
      }
    },
    [onChange]
  );

  return (
    <div className="space-y-2">
      <div className="flex items-center">
        <Label>{label}</Label>
        {tip}
      </div>
      <Textarea
        value={value}
        onChange={handleChange}
        placeholder={placeholder}
        rows={rows}
        className={error ? "border-destructive" : ""}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
