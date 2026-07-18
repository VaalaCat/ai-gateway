"use client";

import { useTranslations } from "next-intl";

import { AgentURI } from "@/components/business/agent-uri";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { RelayMode } from "@/lib/types";
import { parseOptionalRelayURI } from "@/lib/utils/relay-uri";

export type RelayURIValidation =
  | { normalized: string }
  | { error: "required" | "invalid" | "too_long" };

export function validateRelayURI(raw: string): RelayURIValidation {
  const parsed = parseOptionalRelayURI(raw);
  if ("normalized" in parsed && parsed.normalized === "") return { error: "required" };
  return parsed;
}

interface AgentRelayConfigFieldsProps {
  mode: RelayMode;
  uri: string;
  effectiveRelayURI: string;
  activeStreams: number;
  disabled?: boolean;
  onModeChange: (mode: RelayMode) => void;
  onURIChange: (uri: string) => void;
}

export function AgentRelayConfigFields({
  mode,
  uri,
  effectiveRelayURI,
  activeStreams,
  disabled = false,
  onModeChange,
  onURIChange,
}: AgentRelayConfigFieldsProps) {
  const t = useTranslations("agents.connection");
  const validation = mode === "custom" ? validateRelayURI(uri) : undefined;
  const error = validation && "error" in validation ? validation.error : undefined;
  const errorMessage = error === "required"
    ? t("relayUriRequired")
    : error === "too_long"
      ? t("relayUriTooLong")
      : error
        ? t("relayUriInvalid")
        : "";

  return (
    <FieldGroup className="gap-5">
      <Field data-disabled={disabled || undefined}>
        <FieldLabel id="relay-mode-label">{t("relayMode")}</FieldLabel>
        <ToggleGroup
          type="single"
          variant="outline"
          value={mode}
          disabled={disabled}
          aria-labelledby="relay-mode-label"
          className="max-w-full"
          onValueChange={(value) => {
            if (value) onModeChange(value as RelayMode);
          }}
        >
          <ToggleGroupItem value="inherit">{t("relayModeInherit")}</ToggleGroupItem>
          <ToggleGroupItem value="custom">{t("relayModeCustom")}</ToggleGroupItem>
          <ToggleGroupItem value="disabled">{t("relayModeDisabled")}</ToggleGroupItem>
        </ToggleGroup>
      </Field>

      {mode === "custom" ? (
        <Field data-invalid={Boolean(error) || undefined} data-disabled={disabled || undefined}>
          <FieldLabel htmlFor="agent-relay-uri">{t("relayUri")}</FieldLabel>
          <Input
            id="agent-relay-uri"
            value={uri}
            disabled={disabled}
            aria-invalid={Boolean(error)}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => onURIChange(event.target.value)}
          />
          {errorMessage ? <FieldError>{errorMessage}</FieldError> : null}
        </Field>
      ) : mode === "inherit" ? (
        <Field>
          <FieldLabel>{t("effectiveRelayUri")}</FieldLabel>
          <div className="min-w-0 rounded-md border bg-muted/40 px-3 py-2">
            <TooltipProvider><AgentURI uri={effectiveRelayURI} /></TooltipProvider>
          </div>
        </Field>
      ) : (
        <Field>
          <FieldLabel>{t("relayModeDisabled")}</FieldLabel>
          <FieldDescription>{t("activeStreams", { count: activeStreams })}</FieldDescription>
        </Field>
      )}
    </FieldGroup>
  );
}
