"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

interface SSEEvent {
  timestamp: number;
  data: string;
}

interface SSEViewerProps {
  events: SSEEvent[];
}

function SSEEventRow({ event }: { event: SSEEvent }) {
  const [expanded, setExpanded] = useState(false);

  const time = new Date(event.timestamp).toLocaleTimeString([], {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

  let formatted: string | null = null;
  if (expanded && event.data.startsWith("data: ") && event.data !== "data: [DONE]") {
    try {
      const json = JSON.parse(event.data.slice(6));
      formatted = JSON.stringify(json, null, 2);
    } catch {
      // not JSON
    }
  }

  return (
    <button
      type="button"
      className="w-full text-left rounded px-2 py-1 hover:bg-muted/60 transition-colors overflow-hidden"
      onClick={() => setExpanded(!expanded)}
    >
      <div className="flex items-start gap-1.5 min-w-0">
        <ChevronRight
          className={cn(
            "size-3 shrink-0 mt-0.5 transition-transform text-muted-foreground",
            expanded && "rotate-90"
          )}
        />
        <span className="text-muted-foreground shrink-0 select-none">{time}</span>
        {expanded ? (
          <pre className="whitespace-pre-wrap break-all min-w-0 flex-1">
            {formatted ?? event.data}
          </pre>
        ) : (
          <span className="truncate min-w-0 flex-1 block">{event.data}</span>
        )}
      </div>
    </button>
  );
}

export function SSEViewer({ events }: SSEViewerProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const autoScroll = useRef(true);

  const onScroll = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    autoScroll.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }, []);

  useEffect(() => {
    if (autoScroll.current && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [events]);

  if (events.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">
        No events yet.
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      onScroll={onScroll}
      className="flex-1 min-h-0 min-w-0 overflow-y-auto overflow-x-hidden p-3 font-mono text-xs space-y-0.5"
    >
      {events.map((event, i) => (
        <SSEEventRow key={i} event={event} />
      ))}
    </div>
  );
}
