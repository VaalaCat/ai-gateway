"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { copyToClipboard } from "@/lib/utils/clipboard";
import { Eye, EyeOff, Copy, Check, X } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

// Character sets exclude visually ambiguous characters: 0, O, I, l, 1.
const LOWER = "abcdefghijkmnopqrstuvwxyz"; // missing l
const UPPER = "ABCDEFGHJKLMNPQRSTUVWXYZ"; // missing I, O
const DIGIT = "23456789"; // missing 0, 1
const SYMBOL = "!@#$%^&*-_=+?";

/**
 * Generate a strong random password using Web Crypto.
 * Guarantees: 16 chars by default, ≥1 char from each category, no ambiguous chars.
 */
export function generateStrongPassword(length = 16): string {
  if (length < 4) {
    throw new Error("password length must be ≥ 4 to satisfy category guarantees");
  }
  const all = LOWER + UPPER + DIGIT + SYMBOL;
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  const out: string[] = [
    LOWER[buf[0] % LOWER.length],
    UPPER[buf[1] % UPPER.length],
    DIGIT[buf[2] % DIGIT.length],
    SYMBOL[buf[3] % SYMBOL.length],
  ];
  for (let i = 4; i < length; i++) {
    out.push(all[buf[i] % all.length]);
  }
  // Fisher–Yates shuffle so category-anchor chars aren't always at fixed indices.
  const buf2 = new Uint32Array(length);
  crypto.getRandomValues(buf2);
  for (let i = out.length - 1; i > 0; i--) {
    const j = buf2[i] % (i + 1);
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out.join("");
}

interface GeneratedRevealProps {
  password: string;
  onDismiss: () => void;
}

function GeneratedReveal({ password, onDismiss }: GeneratedRevealProps) {
  const t = useTranslations("users");
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    const ok = await copyToClipboard(password);
    if (ok) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } else {
      toast.error(t("passwordCopyFailed"));
    }
  };

  return (
    <Alert className="bg-blue-50 border-blue-200">
      <AlertTitle>{t("passwordRevealedTitle")}</AlertTitle>
      <AlertDescription className="space-y-2">
        <div className="flex gap-2 items-center">
          <code className="flex-1 font-mono text-sm bg-white border rounded px-2 py-1 break-all">
            {password}
          </code>
          <Button type="button" size="sm" variant="outline" onClick={handleCopy}>
            {copied ? (
              <>
                <Check className="size-3 mr-1" />
                {t("passwordCopied")}
              </>
            ) : (
              <>
                <Copy className="size-3 mr-1" />
                {t("passwordCopy")}
              </>
            )}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={onDismiss} aria-label={t("passwordDismiss")}>
            <X className="size-4" />
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          {t("passwordSavedOnceWarning")}
        </p>
      </AlertDescription>
    </Alert>
  );
}

export interface PasswordInputProps {
  value: string;
  onChange: (v: string) => void;
  /** When true, an empty value is the "do not change" hint (edit mode). */
  emptyMeansUnchanged?: boolean;
  placeholder?: string;
  disabled?: boolean;
}

export function PasswordInput({
  value,
  onChange,
  emptyMeansUnchanged,
  placeholder,
  disabled,
}: PasswordInputProps) {
  const t = useTranslations("users");
  const [revealed, setRevealed] = useState(false);
  const [generated, setGenerated] = useState<string | null>(null);

  const handleGenerate = () => {
    const pwd = generateStrongPassword();
    onChange(pwd);
    setGenerated(pwd);
    setRevealed(true);
  };

  const handleInputChange = (next: string) => {
    if (generated !== null && next !== generated) {
      // User edited away from the generated password — close the reveal panel.
      setGenerated(null);
    }
    onChange(next);
  };

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        <Input
          type={revealed ? "text" : "password"}
          value={value}
          onChange={(e) => handleInputChange(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
        />
        <Button
          type="button"
          variant="outline"
          size="icon"
          onClick={() => setRevealed((r) => !r)}
          aria-label={revealed ? t("passwordHide") : t("passwordReveal")}
          disabled={disabled}
        >
          {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
        </Button>
        <Button type="button" onClick={handleGenerate} disabled={disabled}>
          {t("passwordGenerate")}
        </Button>
      </div>
      {emptyMeansUnchanged && !value && (
        <p className="text-xs text-muted-foreground">
          {t("passwordEmptyMeansUnchanged")}
        </p>
      )}
      {generated !== null && (
        <GeneratedReveal
          password={generated}
          onDismiss={() => setGenerated(null)}
        />
      )}
    </div>
  );
}
