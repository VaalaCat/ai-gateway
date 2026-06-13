"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  useSystemStats,
  useCleanupPreview,
  useCleanup,
  useSettings,
  useUpdateSettings,
} from "@/lib/api/system";
import { RefreshCw, Trash2, Database, Server, Activity, Settings } from "lucide-react";
import { toast } from "sonner";
import { BYOKSettingsCard } from "@/components/system/byok-settings";
import { formatFileSize, formatUptime } from "@/lib/utils/format";

// SettingsGroup 是设置卡内的一个语义小节:小标题 + 内容。
function SettingsGroup({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-4">
      <h3 className="text-sm font-medium">{title}</h3>
      {children}
    </section>
  );
}

// SwitchRow 是"标签 + 说明 在左、开关在右"的一行;移动端长文案换行不挤压开关。
function SwitchRow({
  label,
  desc,
  checked,
  onChange,
}: {
  label: string;
  desc: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0 space-y-0.5">
        <Label>{label}</Label>
        <p className="text-label text-muted-foreground">{desc}</p>
      </div>
      <Switch checked={checked} onCheckedChange={onChange} className="shrink-0" />
    </div>
  );
}

// NumField 是带说明的数字输入项;移动端整宽、桌面定宽,便于塞进两列栅格。
function NumField({
  label,
  desc,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  desc: string;
  value: string;
  min: number;
  max: number;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <p className="text-label text-muted-foreground">{desc}</p>
      <Input
        type="number"
        min={min}
        max={max}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full sm:w-[160px]"
      />
    </div>
  );
}

export default function SystemMaintenancePage() {
  const t = useTranslations("system");
  const { data: stats, refetch, isLoading } = useSystemStats();
  const cleanup = useCleanup();
  const { data: settings } = useSettings();
  const updateSettings = useUpdateSettings();

  const [traceMaxBodyKB, setTraceMaxBodyKB] = useState<number | null>(null);
  const [proxyUrlInput, setProxyUrlInput] = useState<string | null>(null);
  const [fallbackSleepInput, setFallbackSleepInput] = useState<string | null>(null);
  const [maxRetriesPerChannelInput, setMaxRetriesPerChannelInput] = useState<string | null>(null);
  const [retryMaxChannelsInput, setRetryMaxChannelsInput] = useState<string | null>(null);
  const [retryBackoffBaseInput, setRetryBackoffBaseInput] = useState<string | null>(null);
  const [retryBackoffMaxInput, setRetryBackoffMaxInput] = useState<string | null>(null);
  const [breakerThresholdInput, setBreakerThresholdInput] = useState<string | null>(null);
  const [breakerCooldownInput, setBreakerCooldownInput] = useState<string | null>(null);
  const [breakerEnabledInput, setBreakerEnabledInput] = useState<boolean | null>(null);
  const [minQuotaReserveInput, setMinQuotaReserveInput] = useState<string | null>(null);
  const [rateLimiterEnabledInput, setRateLimiterEnabledInput] = useState<boolean | null>(null);
  const [sseKeepaliveInput, setSseKeepaliveInput] = useState<string | null>(null);
  const [queueTimeInput, setQueueTimeInput] = useState<string | null>(null);
  const [cleanupTarget, setCleanupTarget] = useState("traces");
  const [retainDays, setRetainDays] = useState(30);
  const [showPreview, setShowPreview] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const { data: preview } = useCleanupPreview(
    cleanupTarget,
    retainDays,
    showPreview,
  );

  const currentTraceKB = settings?.settings?.trace_max_body_size
    ? Math.round(Number(settings.settings.trace_max_body_size) / 1024)
    : 64;
  const displayKB = traceMaxBodyKB ?? currentTraceKB;
  const traceHasChanges = displayKB !== currentTraceKB;

  const currentProxyUrl = settings?.settings?.proxy_url ?? "";
  const displayProxyUrl = proxyUrlInput ?? currentProxyUrl;
  const proxyHasChanges = displayProxyUrl !== currentProxyUrl;

  const [pricingPriorityInput, setPricingPriorityInput] = useState<string | null>(null);
  const [pricingThresholdInput, setPricingThresholdInput] = useState<string | null>(null);
  const currentPricingPriority =
    settings?.settings?.pricing_source_priority ?? "models.dev,basellm";
  const currentPricingThreshold =
    settings?.settings?.pricing_disagreement_threshold ?? "0.2";
  const displayPricingPriority = pricingPriorityInput ?? currentPricingPriority;
  const displayPricingThreshold = pricingThresholdInput ?? currentPricingThreshold;
  const pricingPriorityHasChanges = displayPricingPriority !== currentPricingPriority;
  const pricingThresholdHasChanges = displayPricingThreshold !== currentPricingThreshold;


  const currentRegistrationEnabled =
    settings?.settings?.registration_enabled === "true";
  const [registrationInput, setRegistrationInput] = useState<boolean | null>(
    null,
  );
  const displayRegistrationEnabled =
    registrationInput ?? currentRegistrationEnabled;
  const registrationHasChanges =
    displayRegistrationEnabled !== currentRegistrationEnabled;

  const currentAffinityEnabled =
    settings?.settings?.affinity_enabled === "1";
  const [affinityInput, setAffinityInput] = useState<boolean | null>(null);
  const displayAffinityEnabled = affinityInput ?? currentAffinityEnabled;

  const currentAffinityTTL = settings?.settings?.affinity_ttl_sec ?? "300";
  const [affinityTTLInput, setAffinityTTLInput] = useState<string | null>(null);
  const displayAffinityTTL = affinityTTLInput ?? currentAffinityTTL;

  const affinityHasChanges =
    displayAffinityEnabled !== currentAffinityEnabled ||
    displayAffinityTTL !== currentAffinityTTL;

  const currentFallbackSleepMs = settings?.settings?.fallback_sleep_ms
    ? Number(settings.settings.fallback_sleep_ms)
    : 1000;
  const displayFallbackSleep = fallbackSleepInput ?? String(currentFallbackSleepMs);
  const fallbackSleepHasChanges = displayFallbackSleep !== String(currentFallbackSleepMs);

  const currentMaxRetriesPerChannel = settings?.settings?.max_retries_per_channel
    ? Number(settings.settings.max_retries_per_channel)
    : 2;
  const displayMaxRetriesPerChannel = maxRetriesPerChannelInput ?? String(currentMaxRetriesPerChannel);
  const maxRetriesPerChannelHasChanges = displayMaxRetriesPerChannel !== String(currentMaxRetriesPerChannel);

  const currentRetryMaxChannels = settings?.settings?.retry_max_channels
    ? Number(settings.settings.retry_max_channels)
    : 5;
  const displayRetryMaxChannels = retryMaxChannelsInput ?? String(currentRetryMaxChannels);
  const retryMaxChannelsHasChanges = displayRetryMaxChannels !== String(currentRetryMaxChannels);

  const currentRetryBackoffBase = settings?.settings?.retry_backoff_base_ms
    ? Number(settings.settings.retry_backoff_base_ms)
    : 200;
  const displayRetryBackoffBase = retryBackoffBaseInput ?? String(currentRetryBackoffBase);
  const retryBackoffBaseHasChanges = displayRetryBackoffBase !== String(currentRetryBackoffBase);

  const currentRetryBackoffMax = settings?.settings?.retry_backoff_max_ms
    ? Number(settings.settings.retry_backoff_max_ms)
    : 2000;
  const displayRetryBackoffMax = retryBackoffMaxInput ?? String(currentRetryBackoffMax);
  const retryBackoffMaxHasChanges = displayRetryBackoffMax !== String(currentRetryBackoffMax);

  const currentBreakerThreshold = settings?.settings?.breaker_threshold
    ? Number(settings.settings.breaker_threshold)
    : 5;
  const displayBreakerThreshold = breakerThresholdInput ?? String(currentBreakerThreshold);
  const breakerThresholdHasChanges = displayBreakerThreshold !== String(currentBreakerThreshold);

  const currentBreakerCooldown = settings?.settings?.breaker_cooldown_ms
    ? Number(settings.settings.breaker_cooldown_ms)
    : 30000;
  const displayBreakerCooldown = breakerCooldownInput ?? String(currentBreakerCooldown);
  const breakerCooldownHasChanges = displayBreakerCooldown !== String(currentBreakerCooldown);

  const currentBreakerEnabled = settings?.settings?.breaker_enabled !== "0";
  const displayBreakerEnabled = breakerEnabledInput ?? currentBreakerEnabled;
  const breakerEnabledHasChanges = displayBreakerEnabled !== currentBreakerEnabled;

  const currentMinQuotaReserve = settings?.settings?.min_quota_reserve
    ? Number(settings.settings.min_quota_reserve)
    : 0;
  const displayMinQuotaReserve = minQuotaReserveInput ?? String(currentMinQuotaReserve);
  const minQuotaReserveHasChanges = displayMinQuotaReserve !== String(currentMinQuotaReserve);

  // 请求级限流的三项全局设置。rate_limiter_enabled 后端存 "0"/"1"（默认 1）。
  const currentRateLimiterEnabled = settings?.settings?.rate_limiter_enabled !== "0";
  const displayRateLimiterEnabled = rateLimiterEnabledInput ?? currentRateLimiterEnabled;
  const rateLimiterEnabledHasChanges = displayRateLimiterEnabled !== currentRateLimiterEnabled;

  const currentSseKeepalive = settings?.settings?.sse_keepalive_ms
    ? Number(settings.settings.sse_keepalive_ms)
    : 15000;
  const displaySseKeepalive = sseKeepaliveInput ?? String(currentSseKeepalive);
  const sseKeepaliveHasChanges = displaySseKeepalive !== String(currentSseKeepalive);

  const currentQueueTime = settings?.settings?.queue_time_ms
    ? Number(settings.settings.queue_time_ms)
    : 120000;
  const displayQueueTime = queueTimeInput ?? String(currentQueueTime);
  const queueTimeHasChanges = displayQueueTime !== String(currentQueueTime);

  const currentAutoCreate = settings?.settings?.oauth_auto_create === "true";
  const [autoCreateInput, setAutoCreateInput] = useState<boolean | null>(null);
  const displayAutoCreate = autoCreateInput ?? currentAutoCreate;
  const autoCreateHasChanges = displayAutoCreate !== currentAutoCreate;

  const currentInviteEnabled = settings?.settings?.invite_enabled === "true";
  const [inviteEnabledInput, setInviteEnabledInput] = useState<boolean | null>(null);
  const displayInviteEnabled = inviteEnabledInput ?? currentInviteEnabled;
  const inviteEnabledHasChanges = displayInviteEnabled !== currentInviteEnabled;

  const currentInviteMaxCodes = settings?.settings?.invite_user_max_codes ?? "5";
  const [inviteMaxCodesInput, setInviteMaxCodesInput] = useState<string | null>(null);
  const displayInviteMaxCodes = inviteMaxCodesInput ?? currentInviteMaxCodes;
  const inviteMaxCodesHasChanges = displayInviteMaxCodes !== currentInviteMaxCodes;

  const currentInviteMaxUses = settings?.settings?.invite_user_max_uses ?? "1";
  const [inviteMaxUsesInput, setInviteMaxUsesInput] = useState<string | null>(null);
  const displayInviteMaxUses = inviteMaxUsesInput ?? currentInviteMaxUses;
  const inviteMaxUsesHasChanges = displayInviteMaxUses !== currentInviteMaxUses;

  const hasChanges =
    traceHasChanges ||
    proxyHasChanges ||
    registrationHasChanges ||
    autoCreateHasChanges ||
    inviteEnabledHasChanges ||
    inviteMaxCodesHasChanges ||
    inviteMaxUsesHasChanges ||
    fallbackSleepHasChanges ||
    affinityHasChanges ||
    maxRetriesPerChannelHasChanges ||
    retryMaxChannelsHasChanges ||
    retryBackoffBaseHasChanges ||
    retryBackoffMaxHasChanges ||
    breakerThresholdHasChanges ||
    breakerCooldownHasChanges ||
    breakerEnabledHasChanges ||
    minQuotaReserveHasChanges ||
    rateLimiterEnabledHasChanges ||
    sseKeepaliveHasChanges ||
    queueTimeHasChanges ||
    pricingPriorityHasChanges ||
    pricingThresholdHasChanges;

  const handleSaveSettings = () => {
    const updates: Record<string, string> = {};
    if (traceHasChanges) {
      updates.trace_max_body_size = String(displayKB * 1024);
    }
    if (proxyHasChanges) {
      updates.proxy_url = displayProxyUrl;
    }
    if (registrationHasChanges) {
      updates.registration_enabled = String(displayRegistrationEnabled);
    }
    if (autoCreateHasChanges) {
      updates.oauth_auto_create = String(displayAutoCreate);
    }
    if (inviteEnabledHasChanges) {
      updates.invite_enabled = String(displayInviteEnabled);
    }
    if (inviteMaxCodesHasChanges) {
      const n = Number(displayInviteMaxCodes);
      if (!Number.isInteger(n) || n < 0 || n > 10000) {
        toast.error(t("inviteMaxCodesRangeError"));
        return;
      }
      updates.invite_user_max_codes = String(n);
    }
    if (inviteMaxUsesHasChanges) {
      const n = Number(displayInviteMaxUses);
      if (!Number.isInteger(n) || n < 1 || n > 10000) {
        toast.error(t("inviteMaxUsesRangeError"));
        return;
      }
      updates.invite_user_max_uses = String(n);
    }
    if (fallbackSleepHasChanges) {
      const n = Number(fallbackSleepInput);
      if (!Number.isFinite(n) || n < 0 || n > 60000) {
        toast.error(t("fallbackSleepRangeError"));
        return;
      }
      updates.fallback_sleep_ms = String(n);
    }
    if (maxRetriesPerChannelHasChanges) {
      const n = Number(maxRetriesPerChannelInput);
      if (!Number.isFinite(n) || n < 0 || n > 10) {
        toast.error(t("maxRetriesPerChannelRangeError"));
        return;
      }
      updates.max_retries_per_channel = String(n);
    }
    if (retryMaxChannelsHasChanges) {
      const n = Number(retryMaxChannelsInput);
      if (!Number.isFinite(n) || n < 1 || n > 100) {
        toast.error(t("retryMaxChannelsRangeError"));
        return;
      }
      updates.retry_max_channels = String(n);
    }
    if (retryBackoffBaseHasChanges) {
      const n = Number(retryBackoffBaseInput);
      if (!Number.isFinite(n) || n < 0 || n > 60000) {
        toast.error(t("retryBackoffBaseRangeError"));
        return;
      }
      updates.retry_backoff_base_ms = String(n);
    }
    if (retryBackoffMaxHasChanges) {
      const n = Number(retryBackoffMaxInput);
      if (!Number.isFinite(n) || n < 0 || n > 60000) {
        toast.error(t("retryBackoffMaxRangeError"));
        return;
      }
      updates.retry_backoff_max_ms = String(n);
    }
    if (breakerThresholdHasChanges) {
      const n = Number(breakerThresholdInput);
      if (!Number.isFinite(n) || n < 1 || n > 1000) {
        toast.error(t("breakerThresholdRangeError"));
        return;
      }
      updates.breaker_threshold = String(n);
    }
    if (breakerCooldownHasChanges) {
      const n = Number(breakerCooldownInput);
      if (!Number.isFinite(n) || n < 0 || n > 3600000) {
        toast.error(t("breakerCooldownRangeError"));
        return;
      }
      updates.breaker_cooldown_ms = String(n);
    }
    if (breakerEnabledHasChanges) {
      updates.breaker_enabled = displayBreakerEnabled ? "1" : "0";
    }
    if (minQuotaReserveHasChanges) {
      updates.min_quota_reserve = String(Number(minQuotaReserveInput) || 0);
    }
    if (rateLimiterEnabledHasChanges) {
      updates.rate_limiter_enabled = displayRateLimiterEnabled ? "1" : "0";
    }
    if (sseKeepaliveHasChanges) {
      const n = Number(sseKeepaliveInput);
      if (!Number.isFinite(n) || n < 1000 || n > 60000) {
        toast.error(t("sseKeepaliveRangeError"));
        return;
      }
      updates.sse_keepalive_ms = String(n);
    }
    if (queueTimeHasChanges) {
      const n = Number(queueTimeInput);
      if (!Number.isFinite(n) || n < 0 || n > 600000) {
        toast.error(t("queueTimeRangeError"));
        return;
      }
      updates.queue_time_ms = String(n);
    }
    if (affinityHasChanges) {
      updates.affinity_enabled = displayAffinityEnabled ? "1" : "0";
      updates.affinity_ttl_sec = String(parseInt(displayAffinityTTL, 10) || 300);
    }
    if (pricingPriorityHasChanges) {
      updates.pricing_source_priority = displayPricingPriority;
    }
    if (pricingThresholdHasChanges) {
      updates.pricing_disagreement_threshold = displayPricingThreshold;
    }
    if (Object.keys(updates).length === 0) return;

    updateSettings.mutate(
      { settings: updates },
      {
        onSuccess: () => {
          toast.success(t("settingsSaved"));
          setTraceMaxBodyKB(null);
          setProxyUrlInput(null);
          setRegistrationInput(null);
          setAutoCreateInput(null);
          setInviteEnabledInput(null);
          setInviteMaxCodesInput(null);
          setInviteMaxUsesInput(null);
          setFallbackSleepInput(null);
          setAffinityInput(null);
          setAffinityTTLInput(null);
          setMaxRetriesPerChannelInput(null);
          setRetryMaxChannelsInput(null);
          setRetryBackoffBaseInput(null);
          setRetryBackoffMaxInput(null);
          setBreakerThresholdInput(null);
          setBreakerCooldownInput(null);
          setBreakerEnabledInput(null);
          setMinQuotaReserveInput(null);
          setRateLimiterEnabledInput(null);
          setSseKeepaliveInput(null);
          setQueueTimeInput(null);
          setPricingPriorityInput(null);
          setPricingThresholdInput(null);
        },
        onError: () => {
          toast.error(t("settingsSaveFailed"));
        },
      },
    );
  };

  const handlePreview = () => {
    setShowPreview(true);
  };

  const handleCleanup = () => {
    cleanup.mutate(
      { target: cleanupTarget, retain_days: retainDays },
      {
        onSuccess: (data) => {
          toast.success(t("cleanupSuccess", { count: data.deleted }));
          setConfirmOpen(false);
          setShowPreview(false);
          refetch();
        },
        onError: () => {
          toast.error(t("cleanupFailed"));
        },
      },
    );
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <Button variant="outline" size="sm" onClick={() => refetch()}>
          <RefreshCw
            className={`h-4 w-4 mr-2 ${isLoading ? "animate-spin" : ""}`}
          />
          {t("refresh")}
        </Button>
      </div>

      {/* System Info */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Server className="h-5 w-5" />
            {t("systemInfo")}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {stats?.system && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <div>
                <p className="text-label text-muted-foreground">{t("version")}</p>
                <p className="font-mono">{stats.system.version}</p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">
                  {t("goVersion")}
                </p>
                <p className="font-mono">{stats.system.go_version}</p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">{t("uptime")}</p>
                <p className="font-mono">
                  {formatUptime(stats.system.uptime_sec)}
                </p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">
                  {t("onlineAgents")}
                </p>
                <p className="font-mono">{stats.system.online_agents}</p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">
                  {t("memoryAlloc")}
                </p>
                <p className="font-mono">
                  {formatFileSize(stats.system.memory_alloc)}
                </p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">
                  {t("memorySys")}
                </p>
                <p className="font-mono">
                  {formatFileSize(stats.system.memory_sys)}
                </p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">{t("gcCount")}</p>
                <p className="font-mono">{stats.system.num_gc}</p>
              </div>
              <div>
                <p className="text-label text-muted-foreground">
                  {t("goroutines")}
                </p>
                <p className="font-mono">{stats.system.num_goroutine}</p>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* System Settings */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Settings className="h-5 w-5" />
            {t("settings")}
          </CardTitle>
          <CardDescription>{t("settingsDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* 渠道重试与熔断 */}
          <SettingsGroup title={t("resilienceDefaults")}>
            <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
              <NumField
                label={t("fallbackSleep")}
                desc={t("fallbackSleepDesc")}
                value={displayFallbackSleep}
                min={0}
                max={60000}
                onChange={setFallbackSleepInput}
              />
              <NumField
                label={t("maxRetriesPerChannel")}
                desc={t("maxRetriesPerChannelDesc")}
                value={displayMaxRetriesPerChannel}
                min={0}
                max={10}
                onChange={setMaxRetriesPerChannelInput}
              />
              <NumField
                label={t("retryMaxChannels")}
                desc={t("retryMaxChannelsDesc")}
                value={displayRetryMaxChannels}
                min={1}
                max={100}
                onChange={setRetryMaxChannelsInput}
              />
              <NumField
                label={t("retryBackoffBase")}
                desc={t("retryBackoffBaseDesc")}
                value={displayRetryBackoffBase}
                min={0}
                max={60000}
                onChange={setRetryBackoffBaseInput}
              />
              <NumField
                label={t("retryBackoffMax")}
                desc={t("retryBackoffMaxDesc")}
                value={displayRetryBackoffMax}
                min={0}
                max={60000}
                onChange={setRetryBackoffMaxInput}
              />
              <div className="sm:col-span-2">
                <SwitchRow
                  label={t("breakerEnabled")}
                  desc={t("breakerEnabledDesc")}
                  checked={displayBreakerEnabled}
                  onChange={setBreakerEnabledInput}
                />
              </div>
              <NumField
                label={t("breakerThreshold")}
                desc={t("breakerThresholdDesc")}
                value={displayBreakerThreshold}
                min={1}
                max={1000}
                onChange={setBreakerThresholdInput}
              />
              <NumField
                label={t("breakerCooldown")}
                desc={t("breakerCooldownDesc")}
                value={displayBreakerCooldown}
                min={0}
                max={3600000}
                onChange={setBreakerCooldownInput}
              />
            </div>
          </SettingsGroup>

          <Separator />

          {/* 额度管控 */}
          <SettingsGroup title={t("secQuotaGate")}>
            <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
              <NumField
                label={t("minQuotaReserve")}
                desc={t("minQuotaReserveDesc")}
                value={displayMinQuotaReserve}
                min={0}
                max={1000000000}
                onChange={setMinQuotaReserveInput}
              />
            </div>
          </SettingsGroup>

          <Separator />

          {/* 请求级限流 */}
          <SettingsGroup title={t("secRateLimiter")}>
            <SwitchRow
              label={t("rateLimiterEnabled")}
              desc={t("rateLimiterEnabledDesc")}
              checked={displayRateLimiterEnabled}
              onChange={setRateLimiterEnabledInput}
            />
            {displayRateLimiterEnabled && (
              <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
                <NumField
                  label={t("sseKeepalive")}
                  desc={t("sseKeepaliveDesc")}
                  value={displaySseKeepalive}
                  min={1000}
                  max={60000}
                  onChange={setSseKeepaliveInput}
                />
                <NumField
                  label={t("queueTime")}
                  desc={t("queueTimeDesc")}
                  value={displayQueueTime}
                  min={0}
                  max={600000}
                  onChange={setQueueTimeInput}
                />
              </div>
            )}
          </SettingsGroup>

          <Separator />

          {/* 价格同步 */}
          <SettingsGroup title={t("pricingSyncSettings")}>
            <div className="space-y-1.5">
              <Label className="text-xs">{t("pricingSourcePriority")}</Label>
              <Input
                value={displayPricingPriority}
                placeholder="models.dev,basellm"
                onChange={(e) => setPricingPriorityInput(e.target.value)}
                className="w-full max-w-md"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">{t("pricingDisagreementThreshold")}</Label>
              <Input
                type="number"
                step="0.05"
                min="0"
                max="1"
                value={displayPricingThreshold}
                onChange={(e) => setPricingThresholdInput(e.target.value)}
                className="w-full sm:w-[160px]"
              />
            </div>
          </SettingsGroup>

          <Separator />

          {/* 路由粘性 */}
          <SettingsGroup title={t("secAffinity")}>
            <SwitchRow
              label={t("affinityEnabled")}
              desc={t("affinityEnabledDesc")}
              checked={displayAffinityEnabled}
              onChange={setAffinityInput}
            />
            {displayAffinityEnabled && (
              <NumField
                label={t("affinityTTL")}
                desc={t("affinityTTLDesc")}
                value={displayAffinityTTL}
                min={0}
                max={86400}
                onChange={setAffinityTTLInput}
              />
            )}
          </SettingsGroup>

          <Separator />

          {/* 诊断 Trace */}
          <SettingsGroup title={t("secTrace")}>
            <div className="space-y-1.5">
              <Label>{t("traceMaxBodySize")}</Label>
              <p className="text-label text-muted-foreground">
                {t("traceMaxBodySizeDesc")}
              </p>
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min={4}
                  max={16384}
                  value={displayKB}
                  onChange={(e) => setTraceMaxBodyKB(Number(e.target.value))}
                  className="w-full sm:w-[160px]"
                />
                <span className="text-label text-muted-foreground">
                  {t("traceMaxBodySizeUnit")}
                </span>
              </div>
              <p className="text-meta text-muted-foreground">
                {t("traceMaxBodySizeRange")}
              </p>
            </div>
          </SettingsGroup>

          <Separator />

          {/* 注册与登录 */}
          <SettingsGroup title={t("secRegistration")}>
            <SwitchRow
              label={t("registrationEnabled")}
              desc={t("registrationEnabledDesc")}
              checked={displayRegistrationEnabled}
              onChange={setRegistrationInput}
            />
            <SwitchRow
              label={t("oauthAutoCreate")}
              desc={t("oauthAutoCreateDesc")}
              checked={displayAutoCreate}
              onChange={setAutoCreateInput}
            />
          </SettingsGroup>

          <Separator />

          {/* 邀请注册 */}
          <SettingsGroup title={t("secInvite")}>
            <SwitchRow
              label={t("inviteEnabled")}
              desc={t("inviteEnabledDesc")}
              checked={displayInviteEnabled}
              onChange={setInviteEnabledInput}
            />
            {displayInviteEnabled && (
              <>
                <NumField
                  label={t("inviteMaxCodes")}
                  desc={t("inviteMaxCodesDesc")}
                  value={displayInviteMaxCodes}
                  min={0}
                  max={10000}
                  onChange={setInviteMaxCodesInput}
                />
                <NumField
                  label={t("inviteMaxUses")}
                  desc={t("inviteMaxUsesDesc")}
                  value={displayInviteMaxUses}
                  min={1}
                  max={10000}
                  onChange={setInviteMaxUsesInput}
                />
              </>
            )}
          </SettingsGroup>

          <Separator />

          {/* 网络 */}
          <SettingsGroup title={t("secNetwork")}>
            <div className="space-y-1.5">
              <Label>{t("proxyUrl")}</Label>
              <p className="text-label text-muted-foreground">
                {t("proxyUrlDesc")}
              </p>
              <Input
                type="text"
                placeholder={t("proxyUrlPlaceholder")}
                value={displayProxyUrl}
                onChange={(e) => setProxyUrlInput(e.target.value)}
                className="w-full max-w-md"
              />
            </div>
          </SettingsGroup>

          <div className="flex justify-end pt-2">
            <Button
              onClick={handleSaveSettings}
              disabled={!hasChanges || updateSettings.isPending}
            >
              {t("saveSettings")}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* BYOK Settings */}
      <BYOKSettingsCard />

      {/* Database Stats */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="h-5 w-5" />
            {t("databaseStats")}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("tableName")}</TableHead>
                <TableHead className="text-right">{t("rowCount")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {stats?.tables?.map((table) => (
                <TableRow key={table.name}>
                  <TableCell className="font-mono">{table.name}</TableCell>
                  <TableCell className="text-right font-mono">
                    {table.count.toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* Data Cleanup */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Trash2 className="h-5 w-5" />
            {t("dataCleanup")}
          </CardTitle>
          <CardDescription>{t("dataCleanupDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-end gap-4 flex-wrap">
            <div className="space-y-2">
              <Label>{t("cleanupTarget")}</Label>
              <Select
                value={cleanupTarget}
                onValueChange={(v) => {
                  setCleanupTarget(v);
                  setShowPreview(false);
                }}
              >
                <SelectTrigger className="w-[180px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="traces">{t("traceData")}</SelectItem>
                  <SelectItem value="logs">{t("logData")}</SelectItem>
                  <SelectItem value="hourly_buckets">{t("hourlyBucketData")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>{t("retainDays")}</Label>
              <Input
                type="number"
                min={1}
                value={retainDays}
                onChange={(e) => {
                  setRetainDays(Number(e.target.value));
                  setShowPreview(false);
                }}
                className="w-[120px]"
              />
            </div>
            <Button variant="outline" onClick={handlePreview}>
              <Activity className="h-4 w-4 mr-2" />
              {t("preview")}
            </Button>
          </div>

          {cleanupTarget === "hourly_buckets" && (
            <p className="text-xs text-muted-foreground">{t("cleanupHourlyHint")}</p>
          )}

          {preview && showPreview && (
            <div className="rounded-md border p-4 space-y-2">
              <p>
                {t("totalRecords")}:{" "}
                <span className="font-mono">
                  {preview.total.toLocaleString()}
                </span>
              </p>
              <p>
                {t("toDelete")}:{" "}
                <span className="font-mono text-destructive">
                  {preview.to_delete.toLocaleString()}
                </span>
              </p>
              <Button
                variant="destructive"
                disabled={preview.to_delete === 0}
                onClick={() => setConfirmOpen(true)}
              >
                <Trash2 className="h-4 w-4 mr-2" />
                {t("executeCleanup")}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Confirm Dialog */}
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("confirmCleanup")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("confirmCleanupDesc", {
                count: preview?.to_delete ?? 0,
                target:
                  cleanupTarget === "traces"
                    ? t("traceData")
                    : cleanupTarget === "logs"
                      ? t("logData")
                      : t("hourlyBucketData"),
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={handleCleanup}>
              {t("confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
