"use client";

import { useTranslations } from "next-intl";

import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { STATUS } from "@/lib/constants";
import type { Token } from "@/lib/types";

export function findValidMarketplaceTokens(tokens: Token[], nowSeconds: number) {
  return tokens.filter(
    (token) =>
      token.status === STATUS.ENABLED &&
      (token.expired_at <= 0 || token.expired_at >= nowSeconds),
  );
}

interface TokenPickerProps {
  tokens: Token[];
  selectedTokenId?: number;
  isLoading: boolean;
  allowGlobal?: boolean;
  onChange: (tokenId: number | undefined) => void;
}

const GLOBAL_VALUE = "__global__";

export function TokenPicker({
  tokens,
  selectedTokenId,
  isLoading,
  allowGlobal = false,
  onChange,
}: TokenPickerProps) {
  const t = useTranslations("modelMarketplace");

  return (
    <Field className="max-w-sm">
      <FieldLabel htmlFor="model-marketplace-token">{t("tokenLabel")}</FieldLabel>
      {isLoading ? (
        <Skeleton className="h-9 w-full" />
      ) : (
        <Select
          value={selectedTokenId ? String(selectedTokenId) : allowGlobal ? GLOBAL_VALUE : ""}
          onValueChange={(value) => onChange(value === GLOBAL_VALUE ? undefined : Number(value))}
          disabled={tokens.length === 0 && !allowGlobal}
        >
          <SelectTrigger
            id="model-marketplace-token"
            className="w-full"
            aria-label={t("tokenLabel")}
          >
            <SelectValue placeholder={t("tokenPlaceholder")} />
          </SelectTrigger>
          <SelectContent position="popper">
            <SelectGroup>
              {allowGlobal ? (
                <SelectItem value={GLOBAL_VALUE}>{t("adminGlobalOption")}</SelectItem>
              ) : null}
              {tokens.map((token) => (
                <SelectItem key={token.id} value={String(token.id)}>
                  {token.name}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      )}
      {!isLoading && tokens.length > 1 && !selectedTokenId ? (
        <FieldDescription>{t("chooseTokenDescription")}</FieldDescription>
      ) : (
        <FieldDescription>{t("tokenDescription")}</FieldDescription>
      )}
    </Field>
  );
}
