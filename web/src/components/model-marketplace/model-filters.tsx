"use client";

import { useTranslations } from "next-intl";

import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ModelMarketplaceKind } from "@/lib/api/model-marketplace";

interface ModelFiltersProps {
  search: string;
  provider: string;
  kind: ModelMarketplaceKind;
  providers: string[];
  disabled?: boolean;
  onSearchChange: (value: string) => void;
  onProviderChange: (value: string) => void;
  onKindChange: (value: ModelMarketplaceKind) => void;
}

const ALL_VALUE = "__all__";

export function ModelFilters({
  search,
  provider,
  kind,
  providers,
  disabled = false,
  onSearchChange,
  onProviderChange,
  onKindChange,
}: ModelFiltersProps) {
  const t = useTranslations("modelMarketplace");

  return (
    <FieldGroup className="grid gap-3 md:grid-cols-[minmax(0,1fr)_12rem_12rem]">
      <Field>
        <FieldLabel htmlFor="model-marketplace-search" className="sr-only">
          {t("searchLabel")}
        </FieldLabel>
        <Input
          id="model-marketplace-search"
          type="search"
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          placeholder={t("searchPlaceholder")}
          aria-label={t("searchLabel")}
          disabled={disabled}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor="model-marketplace-provider" className="sr-only">
          {t("providerLabel")}
        </FieldLabel>
        <Select
          value={provider || ALL_VALUE}
          onValueChange={(value) => onProviderChange(value === ALL_VALUE ? "" : value)}
          disabled={disabled}
        >
          <SelectTrigger
            id="model-marketplace-provider"
            className="w-full"
            aria-label={t("providerLabel")}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper">
            <SelectGroup>
              <SelectItem value={ALL_VALUE}>{t("allProviders")}</SelectItem>
              {providers.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel htmlFor="model-marketplace-kind" className="sr-only">
          {t("kindLabel")}
        </FieldLabel>
        <Select
          value={kind || ALL_VALUE}
          onValueChange={(value) =>
            onKindChange(value === ALL_VALUE ? "" : (value as ModelMarketplaceKind))
          }
          disabled={disabled}
        >
          <SelectTrigger
            id="model-marketplace-kind"
            className="w-full"
            aria-label={t("kindLabel")}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper">
            <SelectGroup>
              <SelectItem value={ALL_VALUE}>{t("allKinds")}</SelectItem>
              <SelectItem value="real">{t("kind.real")}</SelectItem>
              <SelectItem value="routing">{t("kind.routing")}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
    </FieldGroup>
  );
}
