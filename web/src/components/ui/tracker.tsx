"use client";

import * as React from "react";

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

// Adapted from Tremor Tracker v1.0.0 (Apache-2.0):
// https://github.com/tremorlabs/tremor/blob/main/src/components/Tracker/Tracker.tsx

export interface TrackerBlock {
  key?: React.Key;
  color?: string;
  tooltip?: React.ReactNode;
  ariaLabel: string;
  state?: string;
  inProgress?: boolean;
  indicatorClassName?: string;
}

export interface TrackerProps extends React.HTMLAttributes<HTMLDivElement> {
  data: readonly TrackerBlock[];
  defaultBackgroundColor?: string;
  hoverEffect?: boolean;
  layout?: "fill" | "compact";
}

function TrackerBlockView({
  block,
  defaultBackgroundColor,
  hoverEffect,
  layout,
}: {
  block: TrackerBlock;
  defaultBackgroundColor: string;
  hoverEffect: boolean;
  layout: "fill" | "compact";
}) {
  const trigger = (
    <span
      role="img"
      tabIndex={0}
      aria-label={block.ariaLabel}
      data-state={block.state}
      data-in-progress={String(Boolean(block.inProgress))}
      className={cn(
        "relative flex size-full outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
        block.color ?? defaultBackgroundColor,
        block.inProgress && block.indicatorClassName,
        hoverEffect && "transition-opacity hover:opacity-80 focus-visible:opacity-80",
      )}
    />
  );

  return (
    <div
      data-slot="tracker-block"
      className={cn(
        layout === "compact"
          ? "h-4 w-[5px] shrink-0 overflow-hidden rounded-[1px]"
          : "size-full flex-1 overflow-hidden px-[0.5px] first:rounded-l-[4px] first:pl-0 last:rounded-r-[4px] last:pr-0 sm:px-px",
      )}
    >
      {block.tooltip ? (
        <Tooltip>
          <TooltipTrigger asChild>{trigger}</TooltipTrigger>
          <TooltipContent>{block.tooltip}</TooltipContent>
        </Tooltip>
      ) : trigger}
    </div>
  );
}

export function Tracker({
  className,
  data,
  defaultBackgroundColor = "bg-gray-200 dark:bg-gray-800",
  hoverEffect = true,
  layout = "fill",
  ...props
}: TrackerProps) {
  return (
    <TooltipProvider>
      <div
        data-slot="tracker"
        className={cn(
          "group flex items-center",
          layout === "compact" ? "w-fit gap-0.5" : "h-8 w-full",
          className,
        )}
        {...props}
      >
        {data.map((block, index) => (
          <TrackerBlockView
            key={block.key ?? index}
            block={block}
            defaultBackgroundColor={defaultBackgroundColor}
            hoverEffect={hoverEffect}
            layout={layout}
          />
        ))}
      </div>
    </TooltipProvider>
  );
}
