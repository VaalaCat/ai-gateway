"use client";

import { ChevronRight } from "lucide-react";

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

const MESSAGE_PREVIEW_LENGTH = 30;
const LONG_MESSAGE_LENGTH = 120;

function parseJson(content: string): JsonValue | undefined {
  try {
    return JSON.parse(content) as JsonValue;
  } catch {
    return undefined;
  }
}

function isJsonContainer(
  value: JsonValue,
): value is JsonValue[] | { [key: string]: JsonValue } {
  return typeof value === "object" && value !== null;
}

function jsonPreview(value: JsonValue): string {
  const serialized = JSON.stringify(value);
  const characters = Array.from(serialized);
  return characters.length > LONG_MESSAGE_LENGTH
    ? `${characters.slice(0, MESSAGE_PREVIEW_LENGTH).join("")}…`
    : serialized;
}

function nodeCount(value: JsonValue[] | { [key: string]: JsonValue }): string {
  if (Array.isArray(value)) {
    return `[${value.length} ${value.length === 1 ? "item" : "items"}]`;
  }
  const count = Object.keys(value).length;
  return `{${count} ${count === 1 ? "key" : "keys"}}`;
}

function JsonPrimitive({ value }: { value: Exclude<JsonValue, object> }) {
  return (
    <span className={value === null ? "text-muted-foreground" : "text-foreground"}>
      {JSON.stringify(value)}
    </span>
  );
}

function JsonLabel({ label, isIndex }: { label: string; isIndex: boolean }) {
  return (
    <span className={cn("shrink-0", !isIndex && "font-medium text-foreground")}>
      {isIndex ? `[${label}]` : JSON.stringify(label)}
      {!isIndex ? <span className="text-muted-foreground">:</span> : null}
    </span>
  );
}

function JsonChildren({
  value,
  path,
  isMessagesArray = false,
}: {
  value: JsonValue[] | { [key: string]: JsonValue };
  path: string;
  isMessagesArray?: boolean;
}) {
  const entries = Array.isArray(value)
    ? value.map((child, index) => [String(index), child] as const)
    : Object.entries(value);

  return (
    <div className="ml-1 border-l border-border/60 pl-3">
      {entries.map(([label, child]) => {
        const isIndex = Array.isArray(value);
        const childPath = `${path}.${label}`;

        if (!isJsonContainer(child)) {
          return (
            <div key={childPath} className="flex min-w-max items-baseline gap-1 leading-5">
              <JsonLabel label={label} isIndex={isIndex} />
              <JsonPrimitive value={child} />
            </div>
          );
        }

        const isMessageItem = isMessagesArray;
        const childIsMessagesArray = !isIndex && label === "messages" && Array.isArray(child);
        return (
          <JsonBranch
            key={childPath}
            value={child}
            path={childPath}
            label={label}
            isIndex={isIndex}
            defaultOpen={!isMessageItem}
            showPreview={isMessageItem}
            ariaLabel={isMessageItem ? `messages item ${Number(label) + 1}` : label}
            isMessagesArray={childIsMessagesArray}
          />
        );
      })}
    </div>
  );
}

function JsonBranch({
  value,
  path,
  label,
  isIndex,
  defaultOpen,
  showPreview,
  ariaLabel,
  isMessagesArray,
}: {
  value: JsonValue[] | { [key: string]: JsonValue };
  path: string;
  label: string;
  isIndex: boolean;
  defaultOpen: boolean;
  showPreview: boolean;
  ariaLabel: string;
  isMessagesArray: boolean;
}) {
  const closingToken = Array.isArray(value) ? "]" : "}";

  return (
    <Collapsible defaultOpen={defaultOpen}>
      <CollapsibleTrigger
        aria-label={ariaLabel}
        className="group flex min-w-max items-center gap-1 rounded-sm pr-1 text-left leading-5 outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ChevronRight
          aria-hidden="true"
          className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90"
        />
        <JsonLabel label={label} isIndex={isIndex} />
        {!showPreview ? (
          <span className="text-muted-foreground">{nodeCount(value)}</span>
        ) : null}
        {showPreview ? (
          <span
            data-slot="json-preview"
            className="text-muted-foreground group-data-[state=open]:hidden"
          >
            {jsonPreview(value)}
          </span>
        ) : null}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <JsonChildren value={value} path={path} isMessagesArray={isMessagesArray} />
        <div className="ml-4 leading-5 text-muted-foreground">{closingToken}</div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function JsonTree({ value, className }: { value: JsonValue; className?: string }) {
  if (!isJsonContainer(value)) {
    return (
      <div
        data-slot="trace-json-tree"
        className={cn(
          "max-h-60 w-full min-w-0 max-w-full overflow-auto whitespace-pre font-mono text-xs",
          className,
        )}
      >
        <JsonPrimitive value={value} />
      </div>
    );
  }

  const openingToken = Array.isArray(value) ? "[" : "{";
  const closingToken = Array.isArray(value) ? "]" : "}";

  return (
    <div
      data-slot="trace-json-tree"
      className={cn(
        "max-h-60 w-full min-w-0 max-w-full overflow-auto font-mono text-xs",
        className,
      )}
    >
      <div className="w-max min-w-full whitespace-pre">
        <div className="leading-5 text-muted-foreground">{openingToken}</div>
        <JsonChildren value={value} path="$" />
        <div className="leading-5 text-muted-foreground">{closingToken}</div>
      </div>
    </div>
  );
}

export function TraceContentBlock({
  content,
  className,
}: {
  content: string;
  className?: string;
}) {
  const parsed = parseJson(content);

  if (parsed !== undefined) {
    return <JsonTree value={parsed} className={className} />;
  }

  return (
    <pre
      className={cn(
        "max-h-60 w-full min-w-0 max-w-full overflow-auto whitespace-pre font-mono text-xs",
        className,
      )}
    >
      {content}
    </pre>
  );
}
