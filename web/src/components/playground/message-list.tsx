"use client";

import { useEffect, useRef, useCallback } from "react";
import { MessageBubble } from "./message-bubble";

interface Message {
  role: string;
  content: string;
}

interface MessageListProps {
  messages: Message[];
}

export function MessageList({ messages }: MessageListProps) {
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
  }, [messages]);

  if (messages.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">
        Send a message to start.
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      onScroll={onScroll}
      className="flex-1 min-h-0 overflow-y-auto p-4 space-y-4"
    >
      {messages.map((msg, i) => (
        <MessageBubble
          key={i}
          role={msg.role as "user" | "assistant"}
          content={msg.content}
        />
      ))}
    </div>
  );
}
