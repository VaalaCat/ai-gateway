"use client";

import { useState, useRef, useCallback, useEffect } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Send, Square, Trash2, Settings2, ChevronDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { SearchableSelect } from "@/components/ui/searchable-select";
import { MessageList } from "@/components/playground/message-list";
import { SSEViewer } from "@/components/playground/sse-viewer";
import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { useToken } from "@/lib/api/tokens";
import { CHAT_ROLES, HTTP_HEADERS } from "@/lib/constants";
import { cn } from "@/lib/utils";
import { useIsMobile } from "@/hooks/use-mobile";
import { useUserPref } from "@/hooks/use-user-pref";

// ─── Types ───────────────────────────────────────────────

interface ChatMessage {
  role: string;
  content: string;
}

interface StreamCallbacks {
  onChunk: (text: string) => void;
  onSSE?: (data: string) => void;
  onRawJson?: (chunks: object[]) => void;
}

// ─── Stream helper ───────────────────────────────────────

async function streamChat(
  endpoint: string,
  apiKey: string,
  messages: ChatMessage[],
  model: string,
  params: { temperature: number; max_tokens: number },
  callbacks: StreamCallbacks,
  signal: AbortSignal,
) {
  const res = await fetch(`${endpoint}/v1/chat/completions`, {
    method: "POST",
    headers: {
      [HTTP_HEADERS.CONTENT_TYPE]: "application/json",
      [HTTP_HEADERS.AUTHORIZATION]: `Bearer ${apiKey}`,
    },
    body: JSON.stringify({ model, messages, stream: true, ...params }),
    signal,
  });

  if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`);

  const reader = res.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const allChunks: object[] = [];

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop()!;
    for (const line of lines) {
      if (!line.startsWith("data: ")) continue;
      callbacks.onSSE?.(line);
      if (line === "data: [DONE]") continue;
      try {
        const json = JSON.parse(line.slice(6));
        allChunks.push(json);
        const content = json.choices?.[0]?.delta?.content;
        if (content) callbacks.onChunk(content);
      } catch {
        /* skip malformed */
      }
    }
  }

  if (allChunks.length > 0) callbacks.onRawJson?.(allChunks);
}

// ─── Settings panel (shared between desktop & mobile) ────

function SettingsPanel({
  t,
  selectedTokenId,
  setSelectedTokenId,
  manualKey,
  setManualKey,
  useManualKey,
  setUseManualKey,
  requestTab,
  setRequestTab,
  model,
  setModel,
  manualModelInput,
  setManualModelInput,
  availableModels,
  systemPrompt,
  setSystemPrompt,
  temperature,
  setTemperature,
  maxTokens,
  setMaxTokens,
  requestJson,
  setRequestJson,
}: {
  t: ReturnType<typeof useTranslations<"playground">>;
  selectedTokenId: string;
  setSelectedTokenId: (v: string) => void;
  manualKey: string;
  setManualKey: (v: string) => void;
  useManualKey: boolean;
  setUseManualKey: (v: boolean) => void;
  requestTab: string;
  setRequestTab: (v: string) => void;
  model: string;
  setModel: (v: string) => void;
  manualModelInput: boolean;
  setManualModelInput: (v: boolean) => void;
  availableModels: string[];
  systemPrompt: string;
  setSystemPrompt: (v: string) => void;
  temperature: number;
  setTemperature: (v: number) => void;
  maxTokens: number;
  setMaxTokens: (v: number) => void;
  requestJson: string;
  setRequestJson: (v: string) => void;
}) {
  return (
    <div className="space-y-4">
      {/* Token */}
      <div className="space-y-1.5">
        <Label className="text-xs font-medium">{t("apiKey")}</Label>
        {useManualKey ? (
          <Input
            type="password"
            value={manualKey}
            onChange={(e) => setManualKey(e.target.value)}
            placeholder="sk-..."
            className="h-8 text-sm"
          />
        ) : (
          <EntityPicker
            entity="token"
            value={selectedTokenId}
            onChange={setSelectedTokenId}
            placeholder={t("selectToken")}
            className="w-full h-8"
          />
        )}
        <button
          type="button"
          className="text-xs text-muted-foreground hover:underline"
          onClick={() => setUseManualKey(!useManualKey)}
        >
          {useManualKey ? t("selectToken") : t("manualInput")}
        </button>
      </div>

      {/* Request mode */}
      <Tabs value={requestTab} onValueChange={setRequestTab}>
        <TabsList className="w-full h-8">
          <TabsTrigger value="form" className="flex-1 text-xs">
            {t("formMode")}
          </TabsTrigger>
          <TabsTrigger value="json" className="flex-1 text-xs">
            JSON
          </TabsTrigger>
        </TabsList>

        <TabsContent value="form" className="space-y-3 mt-3">
          <div className="space-y-1.5">
            <Label className="text-xs font-medium">{t("selectModel")}</Label>
            {manualModelInput ? (
              <Input
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="gpt-4o"
                className="h-8 text-sm"
              />
            ) : (
              <SearchableSelect
                value={model}
                onChange={setModel}
                placeholder={t("selectModel")}
                searchPlaceholder={t("selectModel")}
                items={availableModels.map((name) => ({ value: name, label: name }))}
              />
            )}
            <button
              type="button"
              className="text-xs text-muted-foreground hover:underline"
              onClick={() => setManualModelInput(!manualModelInput)}
            >
              {manualModelInput ? t("selectModel") : t("manualInput")}
            </button>
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs font-medium">{t("systemPrompt")}</Label>
            <Textarea
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              placeholder={t("systemPromptPlaceholder")}
              rows={2}
              className="text-sm resize-none"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-xs font-medium">{t("temperature")}</Label>
              <Input
                type="number"
                min={0}
                max={2}
                step={0.1}
                value={temperature}
                onChange={(e) => setTemperature(parseFloat(e.target.value) || 0)}
                className="h-8 text-sm"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs font-medium">{t("maxTokens")}</Label>
              <Input
                type="number"
                min={1}
                max={128000}
                step={1}
                value={maxTokens}
                onChange={(e) => setMaxTokens(parseInt(e.target.value) || 2048)}
                className="h-8 text-sm"
              />
            </div>
          </div>
        </TabsContent>

        <TabsContent value="json" className="mt-3">
          <Textarea
            value={requestJson}
            onChange={(e) => setRequestJson(e.target.value)}
            className="min-h-[200px] font-mono text-xs resize-none"
            spellCheck={false}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// ─── Page ────────────────────────────────────────────────

export default function PlaygroundPage() {
  const t = useTranslations("playground");
  const isMobile = useIsMobile();
  const endpoint = typeof window !== "undefined" ? window.location.origin : "";

  // Token
  const [selectedTokenId, setSelectedTokenId] = useUserPref<string>(
    "playground-token-id",
    "",
  );
  const { data: selectedToken } = useToken(
    selectedTokenId ? Number(selectedTokenId) : 0,
  );
  const [manualKey, setManualKey] = useState("");
  const [useManualKey, setUseManualKey] = useState(false);
  const apiKey = useManualKey ? manualKey : selectedToken?.key ?? "";

  // Model & params
  const [model, setModel] = useUserPref<string>("playground-model", "");
  const [manualModelInput, setManualModelInput] = useState(false);
  const [availableModels, setAvailableModels] = useState<string[]>([]);

  // Fetch models from /v1/models using the selected API token
  useEffect(() => {
    if (!apiKey) {
      setAvailableModels([]);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch(`${endpoint}/v1/models`, {
          headers: { [HTTP_HEADERS.AUTHORIZATION]: `Bearer ${apiKey}` },
        });
        if (!res.ok) return;
        const json = await res.json();
        if (!cancelled && Array.isArray(json.data)) {
          setAvailableModels(json.data.map((m: { id: string }) => m.id));
        }
      } catch {
        /* ignore */
      }
    })();
    return () => { cancelled = true; };
  }, [apiKey, endpoint]);
  const [temperature, setTemperature] = useState(1);
  const [maxTokens, setMaxTokens] = useState(2048);
  const [systemPrompt, setSystemPrompt] = useState("");

  // Request editor
  const [requestTab, setRequestTab] = useState("form");
  const [requestJson, setRequestJson] = useState("");

  // Chat
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  // Response viewer
  const [responseTab, setResponseTab] = useState("render");
  const [sseEvents, setSSEEvents] = useState<{ timestamp: number; data: string }[]>([]);
  const [rawResponse, setRawResponse] = useState("");

  // Mobile settings
  const [settingsOpen, setSettingsOpen] = useState(false);

  // Sync form → JSON
  const buildRequestBody = useCallback(() => {
    const msgs = [...messages];
    if (systemPrompt.trim()) msgs.unshift({ role: CHAT_ROLES.SYSTEM, content: systemPrompt });
    if (input.trim()) msgs.push({ role: CHAT_ROLES.USER, content: input.trim() });
    return { model, messages: msgs, stream: true, temperature, max_tokens: maxTokens };
  }, [model, messages, systemPrompt, temperature, maxTokens, input]);

  useEffect(() => {
    if (requestTab === "json") setRequestJson(JSON.stringify(buildRequestBody(), null, 2));
  }, [requestTab, buildRequestBody]);

  // Send
  const handleSend = useCallback(async () => {
    if (isStreaming) return;
    let sendMessages: ChatMessage[];
    let sendModel = model;
    let sendParams = { temperature, max_tokens: maxTokens };

    if (requestTab === "json" && requestJson.trim()) {
      try {
        const parsed = JSON.parse(requestJson);
        sendMessages = parsed.messages || [];
        sendModel = parsed.model || model;
        sendParams = {
          temperature: parsed.temperature ?? temperature,
          max_tokens: parsed.max_tokens ?? maxTokens,
        };
      } catch {
        toast.error("Invalid JSON");
        return;
      }
    } else {
      if (!input.trim()) return;
      const userMsg: ChatMessage = { role: CHAT_ROLES.USER, content: input.trim() };
      sendMessages = [...messages];
      if (systemPrompt.trim())
        sendMessages = [{ role: CHAT_ROLES.SYSTEM, content: systemPrompt }, ...sendMessages];
      sendMessages.push(userMsg);
      setMessages((prev) => [...prev, userMsg]);
      setInput("");
    }

    setIsStreaming(true);
    setSSEEvents([]);
    setRawResponse("");
    const assistantMsg: ChatMessage = { role: CHAT_ROLES.ASSISTANT, content: "" };
    setMessages((prev) => [...prev, assistantMsg]);
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      await streamChat(endpoint, apiKey, sendMessages, sendModel, sendParams, {
        onChunk: (chunk) => {
          assistantMsg.content += chunk;
          setMessages((prev) => [...prev.slice(0, -1), { ...assistantMsg }]);
        },
        onSSE: (data) => {
          setSSEEvents((prev) => [...prev, { timestamp: Date.now(), data }]);
        },
        onRawJson: (chunks) => {
          setRawResponse(JSON.stringify(chunks, null, 2));
        },
      }, controller.signal);
    } catch (e: unknown) {
      if (e instanceof Error && e.name !== "AbortError") toast.error(e.message || "Error");
    } finally {
      setIsStreaming(false);
      abortRef.current = null;
    }
  }, [input, isStreaming, messages, endpoint, apiKey, model, temperature, maxTokens, requestTab, requestJson, systemPrompt]);

  const handleStop = useCallback(() => abortRef.current?.abort(), []);
  const handleClear = useCallback(() => {
    setMessages([]);
    setSSEEvents([]);
    setRawResponse("");
  }, []);
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSend();
      }
    },
    [handleSend],
  );

  const settingsProps = {
    t, selectedTokenId, setSelectedTokenId,
    manualKey, setManualKey, useManualKey, setUseManualKey,
    requestTab, setRequestTab, model, setModel,
    manualModelInput, setManualModelInput, availableModels, systemPrompt, setSystemPrompt,
    temperature, setTemperature, maxTokens, setMaxTokens,
    requestJson, setRequestJson,
  };

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="shrink-0 mb-4">
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground text-sm mt-0.5">{t("description")}</p>
      </div>

      {/* Body */}
      <div className={cn("flex-1 min-h-0 flex gap-4", isMobile ? "flex-col" : "flex-row")}>
        {/* Settings */}
        {isMobile ? (
          <div className="shrink-0">
            <Collapsible open={settingsOpen} onOpenChange={setSettingsOpen}>
              <CollapsibleTrigger asChild>
                <Button variant="outline" size="sm" className="w-full justify-between">
                  <span className="flex items-center gap-2">
                    <Settings2 className="size-4" />
                    <span className="text-sm">{t("title")}</span>
                    {model && (
                      <span className="text-muted-foreground text-xs font-normal">
                        · {model}
                      </span>
                    )}
                  </span>
                  <ChevronDown
                    className={cn("size-4 transition-transform", settingsOpen && "rotate-180")}
                  />
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className="mt-2 rounded-lg border bg-card p-4">
                  <SettingsPanel {...settingsProps} />
                </div>
              </CollapsibleContent>
            </Collapsible>
          </div>
        ) : (
          <aside className="w-[300px] shrink-0 overflow-y-auto rounded-lg border bg-card p-4">
            <SettingsPanel {...settingsProps} />
          </aside>
        )}

        {/* Chat panel */}
        <div className="flex-1 min-h-0 flex flex-col rounded-lg border bg-card overflow-hidden">
          {/* Response tabs */}
          <Tabs
            value={responseTab}
            onValueChange={setResponseTab}
            className="flex-1 min-h-0 flex flex-col"
          >
            <div className="shrink-0 border-b px-3 pt-1.5">
              <TabsList className="h-8 bg-transparent p-0 gap-1">
                <TabsTrigger
                  value="render"
                  className="text-xs data-[state=active]:bg-muted rounded-md px-3 h-7"
                >
                  {t("renderView")}
                </TabsTrigger>
                <TabsTrigger
                  value="json"
                  className="text-xs data-[state=active]:bg-muted rounded-md px-3 h-7"
                >
                  JSON
                </TabsTrigger>
                <TabsTrigger
                  value="sse"
                  className="text-xs data-[state=active]:bg-muted rounded-md px-3 h-7"
                >
                  SSE
                </TabsTrigger>
              </TabsList>
            </div>

            <TabsContent value="render" className="flex-1 min-h-0 flex flex-col m-0 p-0">
              <MessageList messages={messages} />
            </TabsContent>

            <TabsContent value="json" className="flex-1 min-h-0 overflow-y-auto m-0 p-4">
              <pre className="font-mono text-xs whitespace-pre-wrap break-all">
                {rawResponse || "No response data yet."}
              </pre>
            </TabsContent>

            <TabsContent value="sse" className="flex-1 min-h-0 flex flex-col m-0 p-0">
              <SSEViewer events={sseEvents} />
            </TabsContent>
          </Tabs>

          {/* Input bar */}
          <div className="shrink-0 border-t p-3">
            <div className="flex gap-2 items-end">
              <Textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={t("placeholder")}
                disabled={isStreaming}
                rows={1}
                className="flex-1 min-h-[40px] max-h-[120px] resize-none text-sm"
              />
              <div className="flex gap-1.5 shrink-0">
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-9"
                  onClick={handleClear}
                  disabled={messages.length === 0 || isStreaming}
                  title={t("clear")}
                >
                  <Trash2 className="size-4" />
                </Button>
                {isStreaming ? (
                  <Button
                    variant="destructive"
                    size="icon"
                    className="size-9"
                    onClick={handleStop}
                    title={t("stop")}
                  >
                    <Square className="size-4" />
                  </Button>
                ) : (
                  <Button
                    size="icon"
                    className="size-9"
                    onClick={handleSend}
                    disabled={!input.trim() && requestTab !== "json"}
                    title={t("send")}
                  >
                    <Send className="size-4" />
                  </Button>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
