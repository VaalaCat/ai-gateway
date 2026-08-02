"use client";

import { useId } from "react";
import { useTranslations } from "next-intl";

import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ENDPOINT_DEFAULTS, ENDPOINT_OPTIONS } from "./types";
import { parseEndpoints, stringifyEndpoints } from "./utils";

export interface ChannelEndpointsEditorProps {
  endpoints: string;
  baseURL: string;
  showEmptyWarning?: boolean;
  onEndpointsChange: (endpoints: string) => void;
}

export function ChannelEndpointsEditor({
  endpoints,
  baseURL,
  showEmptyWarning = true,
  onEndpointsChange,
}: ChannelEndpointsEditorProps) {
  const t = useTranslations("channels");
  const idPrefix = useId();
  const parsedEndpoints = parseEndpoints(endpoints);
  const normalizedBaseURL = baseURL ? baseURL.replace(/\/+$/, "") : "";

  const toggleEndpoint = (key: string, checked: boolean) => {
    const updated = { ...parsedEndpoints };
    if (checked) {
      (updated as Record<string, string>)[key] = ENDPOINT_DEFAULTS[key];
    } else {
      delete (updated as Record<string, string | undefined>)[key];
    }
    onEndpointsChange(stringifyEndpoints(updated));
  };

  const updatePath = (key: string, path: string) => {
    onEndpointsChange(stringifyEndpoints({ ...parsedEndpoints, [key]: path }));
  };

  return (
    <FieldGroup className="gap-3">
      {showEmptyWarning && Object.keys(parsedEndpoints).length === 0 ? (
        <FieldDescription className="text-destructive">{t("encodeEndpointsRequired")}</FieldDescription>
      ) : null}
      {ENDPOINT_OPTIONS.map((option) => {
        const enabled = option.key in parsedEndpoints;
        const path = parsedEndpoints[option.key] || "";
        const fullURL = normalizedBaseURL && path ? `${normalizedBaseURL}${path}` : "";
        const checkboxID = `${idPrefix}-${option.key}`;
        return (
          <Field key={option.key} className="gap-2">
            <Field orientation="horizontal">
              <Checkbox
                id={checkboxID}
                checked={enabled}
                onCheckedChange={(checked) => toggleEndpoint(option.key, !!checked)}
              />
              <FieldLabel htmlFor={checkboxID}>{t(option.labelKey)}</FieldLabel>
            </Field>
            {enabled ? (
              <Field className="pl-6">
                <FieldLabel htmlFor={`${checkboxID}-path`}>{t("endpointPath")}</FieldLabel>
                <Input
                  id={`${checkboxID}-path`}
                  value={path}
                  onChange={(event) => updatePath(option.key, event.target.value)}
                  placeholder={option.default}
                  className="font-mono text-xs"
                />
                {fullURL ? (
                  <FieldDescription className="break-all font-mono">→ {fullURL}</FieldDescription>
                ) : null}
              </Field>
            ) : null}
          </Field>
        );
      })}
    </FieldGroup>
  );
}
