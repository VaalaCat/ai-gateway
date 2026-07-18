"use client";

import * as React from "react";

import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export type SettingNumberInputProps = React.ComponentProps<typeof Input> & {
  unit?: string;
  humanReadable?: string | null;
};

export function SettingNumberInput({
  unit,
  humanReadable,
  className,
  onFocus,
  onBlur,
  onPointerEnter,
  onPointerLeave,
  "aria-describedby": ariaDescribedBy,
  ...inputProps
}: SettingNumberInputProps) {
  const [isFocused, setIsFocused] = React.useState(false);
  const [isHovered, setIsHovered] = React.useState(false);
  const [isDismissed, setIsDismissed] = React.useState(false);
  const descriptionId = React.useId();
  const description = [unit, humanReadable].filter(Boolean).join(", ");
  const inputDescribedBy = [
    ariaDescribedBy,
    description ? descriptionId : undefined,
  ]
    .filter(Boolean)
    .join(" ") || undefined;
  const inputClassName = cn(
    className,
    "min-w-0 font-mono tabular-nums",
  );
  const handleFocus: React.FocusEventHandler<HTMLInputElement> = (event) => {
    setIsDismissed(false);
    setIsFocused(true);
    onFocus?.(event);
  };
  const handleBlur: React.FocusEventHandler<HTMLInputElement> = (event) => {
    setIsFocused(false);
    onBlur?.(event);
  };
  const handlePointerEnter: React.PointerEventHandler<HTMLInputElement> = (
    event,
  ) => {
    if (event.pointerType !== "touch") {
      setIsDismissed(false);
      setIsHovered(true);
    }
    onPointerEnter?.(event);
  };
  const handlePointerLeave: React.PointerEventHandler<HTMLInputElement> = (
    event,
  ) => {
    setIsHovered(false);
    onPointerLeave?.(event);
  };
  const handleEscapeKeyDown = () => {
    setIsDismissed(true);
  };
  const numberInput = (
    <Input
      className={inputClassName}
      aria-describedby={inputDescribedBy}
      onFocus={handleFocus}
      onBlur={handleBlur}
      onPointerEnter={handlePointerEnter}
      onPointerLeave={handlePointerLeave}
      {...inputProps}
    />
  );
  const input = humanReadable !== undefined ? (
    <TooltipProvider>
      <Tooltip
        open={
          Boolean(humanReadable) &&
          !isDismissed &&
          (isFocused || isHovered)
        }
      >
        <TooltipTrigger asChild>{numberInput}</TooltipTrigger>
        {Boolean(humanReadable) ? (
          <TooltipContent
            side="bottom"
            align="start"
            sideOffset={6}
            className="font-mono tabular-nums"
            onEscapeKeyDown={handleEscapeKeyDown}
          >
            {unit ? <span className="sr-only">{unit}, </span> : null}
            {humanReadable}
          </TooltipContent>
        ) : null}
      </Tooltip>
    </TooltipProvider>
  ) : (
    numberInput
  );

  return (
    <div className="flex w-full min-w-0 items-center gap-2">
      {input}
      {description ? (
        <span id={descriptionId} className="sr-only">
          {description}
        </span>
      ) : null}
      {unit ? (
        <span
          aria-hidden="true"
          className="pointer-events-none shrink-0 text-sm text-muted-foreground"
        >
          {unit}
        </span>
      ) : null}
    </div>
  );
}
