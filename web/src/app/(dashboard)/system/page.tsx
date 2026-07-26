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
import { isCleanupPreviewExpired } from "@/lib/utils/cleanup-preview";
import {
  RefreshCw,
  Trash2,
  Database,
  Server,
  Activity,
  Settings,
} from "lucide-react";
import { toast } from "sonner";
import { BYOKSettingsCard } from "@/components/system/byok-settings";
import { AgentRelaySettings } from "@/components/system/agent-relay-settings";
import { SettingNumberInput } from "@/components/system/setting-number-input";
import { LogStorageStatus } from "@/components/system/log-storage-status";
import { SystemMaintenanceTabs } from "@/components/system/system-maintenance-tabs";
import { formatFileSize, formatUptime } from "@/lib/utils/format";
import {
  humanizeSettingNumber,
  type SettingNumberKind,
} from "@/lib/utils/system-setting-number";
import type {
  CleanupPreviewResponse,
  SystemInfo,
  TableStats,
} from "@/lib/types";

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
      <Switch
        checked={checked}
        onCheckedChange={onChange}
        className="shrink-0"
      />
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
  step,
  unit,
  humanizeAs,
  onChange,
}: {
  label: string;
  desc: string;
  value: string;
  min: number;
  max: number;
  step?: number;
  unit?: string;
  humanizeAs?: SettingNumberKind;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <p className="text-label text-muted-foreground">{desc}</p>
      <SettingNumberInput
        type="number"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full sm:w-[160px]"
        unit={unit}
        humanReadable={
          humanizeAs ? humanizeSettingNumber(value, humanizeAs) : undefined
        }
      />
    </div>
  );
}

function SettingsCard({
  children,
  t,
  saveDisabled,
  onSave,
}: {
  children: React.ReactNode;
  t: SystemTranslator;
  saveDisabled: boolean;
  onSave: () => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Settings className="size-5" />
          {t("settings")}
        </CardTitle>
        <CardDescription>{t("settingsDesc")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {children}
        <div className="flex justify-end pt-2">
          <Button onClick={onSave} disabled={saveDisabled}>
            {t("saveSettings")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

type SystemTranslator = ReturnType<typeof useTranslations<"system">>;

interface TextSettingInput {
  value: string;
  change: (value: string) => void;
}

interface BooleanSettingInput {
  value: boolean;
  change: (value: boolean) => void;
}

interface SettingsSaveAction {
  disabled: boolean;
  run: () => void;
}

function SystemInfoCard({
  system,
  t,
}: {
  system?: SystemInfo;
  t: SystemTranslator;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Server className="size-5" />
          {t("systemInfo")}
        </CardTitle>
      </CardHeader>
      <CardContent>
        {system && (
          <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
            <div>
              <p className="text-label text-muted-foreground">{t("version")}</p>
              <p className="font-mono">{system.version}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">
                {t("goVersion")}
              </p>
              <p className="font-mono">{system.go_version}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">{t("uptime")}</p>
              <p className="font-mono">{formatUptime(system.uptime_sec)}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">
                {t("onlineAgents")}
              </p>
              <p className="font-mono">{system.online_agents}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">
                {t("memoryAlloc")}
              </p>
              <p className="font-mono">{formatFileSize(system.memory_alloc)}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">
                {t("memorySys")}
              </p>
              <p className="font-mono">{formatFileSize(system.memory_sys)}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">{t("gcCount")}</p>
              <p className="font-mono">{system.num_gc}</p>
            </div>
            <div>
              <p className="text-label text-muted-foreground">
                {t("goroutines")}
              </p>
              <p className="font-mono">{system.num_goroutine}</p>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

interface RequestPathSettingsDraft {
  fallbackSleep: TextSettingInput;
  maxRetriesPerChannel: TextSettingInput;
  retryMaxChannels: TextSettingInput;
  retryBackoffBase: TextSettingInput;
  retryBackoffMax: TextSettingInput;
  breakerEnabled: BooleanSettingInput;
  breakerThreshold: TextSettingInput;
  breakerCooldown: TextSettingInput;
  rateLimiterEnabled: BooleanSettingInput;
  sseKeepalive: TextSettingInput;
  queueTime: TextSettingInput;
  affinityEnabled: BooleanSettingInput;
  affinityTTL: TextSettingInput;
  proxyUrl: TextSettingInput;
  imageInlineFetchTimeoutSec: TextSettingInput;
  imageInlineMaxBytes: TextSettingInput;
  imageInlineConcurrency: TextSettingInput;
  imageInlineSsrfGuard: BooleanSettingInput;
  imageInlineHostAllowlist: TextSettingInput;
}

function RequestPathSettingsContent({
  draft,
  saveAction,
  t,
}: {
  draft: RequestPathSettingsDraft;
  saveAction: SettingsSaveAction;
  t: SystemTranslator;
}) {
  return (
    <SettingsCard
      t={t}
      saveDisabled={saveAction.disabled}
      onSave={saveAction.run}
    >
      <AgentRelaySettings />
      <Separator />
      <SettingsGroup title={t("resilienceDefaults")}>
        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <NumField
            label={t("fallbackSleep")}
            desc={t("fallbackSleepDesc")}
            value={draft.fallbackSleep.value}
            min={0}
            max={60000}
            unit="ms"
            humanizeAs="milliseconds"
            onChange={draft.fallbackSleep.change}
          />
          <NumField
            label={t("maxRetriesPerChannel")}
            desc={t("maxRetriesPerChannelDesc")}
            value={draft.maxRetriesPerChannel.value}
            min={0}
            max={10}
            onChange={draft.maxRetriesPerChannel.change}
          />
          <NumField
            label={t("retryMaxChannels")}
            desc={t("retryMaxChannelsDesc")}
            value={draft.retryMaxChannels.value}
            min={1}
            max={100}
            onChange={draft.retryMaxChannels.change}
          />
          <NumField
            label={t("retryBackoffBase")}
            desc={t("retryBackoffBaseDesc")}
            value={draft.retryBackoffBase.value}
            min={0}
            max={60000}
            unit="ms"
            humanizeAs="milliseconds"
            onChange={draft.retryBackoffBase.change}
          />
          <NumField
            label={t("retryBackoffMax")}
            desc={t("retryBackoffMaxDesc")}
            value={draft.retryBackoffMax.value}
            min={0}
            max={60000}
            unit="ms"
            humanizeAs="milliseconds"
            onChange={draft.retryBackoffMax.change}
          />
          <div className="sm:col-span-2">
            <SwitchRow
              label={t("breakerEnabled")}
              desc={t("breakerEnabledDesc")}
              checked={draft.breakerEnabled.value}
              onChange={draft.breakerEnabled.change}
            />
          </div>
          <NumField
            label={t("breakerThreshold")}
            desc={t("breakerThresholdDesc")}
            value={draft.breakerThreshold.value}
            min={1}
            max={1000}
            onChange={draft.breakerThreshold.change}
          />
          <NumField
            label={t("breakerCooldown")}
            desc={t("breakerCooldownDesc")}
            value={draft.breakerCooldown.value}
            min={0}
            max={3600000}
            unit="ms"
            humanizeAs="milliseconds"
            onChange={draft.breakerCooldown.change}
          />
        </div>
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secRateLimiter")}>
        <SwitchRow
          label={t("rateLimiterEnabled")}
          desc={t("rateLimiterEnabledDesc")}
          checked={draft.rateLimiterEnabled.value}
          onChange={draft.rateLimiterEnabled.change}
        />
        {draft.rateLimiterEnabled.value && (
          <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
            <NumField
              label={t("sseKeepalive")}
              desc={t("sseKeepaliveDesc")}
              value={draft.sseKeepalive.value}
              min={1000}
              max={60000}
              unit="ms"
              humanizeAs="milliseconds"
              onChange={draft.sseKeepalive.change}
            />
            <NumField
              label={t("queueTime")}
              desc={t("queueTimeDesc")}
              value={draft.queueTime.value}
              min={0}
              max={600000}
              unit="ms"
              humanizeAs="milliseconds"
              onChange={draft.queueTime.change}
            />
          </div>
        )}
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secAffinity")}>
        <SwitchRow
          label={t("affinityEnabled")}
          desc={t("affinityEnabledDesc")}
          checked={draft.affinityEnabled.value}
          onChange={draft.affinityEnabled.change}
        />
        {draft.affinityEnabled.value && (
          <NumField
            label={t("affinityTTL")}
            desc={t("affinityTTLDesc")}
            value={draft.affinityTTL.value}
            min={0}
            max={86400}
            unit="s"
            humanizeAs="seconds"
            onChange={draft.affinityTTL.change}
          />
        )}
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secNetwork")}>
        <div className="flex flex-col gap-1.5">
          <Label>{t("proxyUrl")}</Label>
          <p className="text-label text-muted-foreground">
            {t("proxyUrlDesc")}
          </p>
          <Input
            type="text"
            placeholder={t("proxyUrlPlaceholder")}
            value={draft.proxyUrl.value}
            onChange={(event) => draft.proxyUrl.change(event.target.value)}
            className="w-full max-w-md"
          />
        </div>
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secImageInline")}>
        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <NumField
            label={t("imageInlineFetchTimeoutSec")}
            desc={t("imageInlineFetchTimeoutSecDesc")}
            value={draft.imageInlineFetchTimeoutSec.value}
            min={1}
            max={300}
            unit="s"
            humanizeAs="seconds"
            onChange={draft.imageInlineFetchTimeoutSec.change}
          />
          <NumField
            label={t("imageInlineMaxBytes")}
            desc={t("imageInlineMaxBytesDesc")}
            value={draft.imageInlineMaxBytes.value}
            min={1024}
            max={104857600}
            unit="bytes"
            humanizeAs="bytes"
            onChange={draft.imageInlineMaxBytes.change}
          />
          <NumField
            label={t("imageInlineConcurrency")}
            desc={t("imageInlineConcurrencyDesc")}
            value={draft.imageInlineConcurrency.value}
            min={1}
            max={32}
            onChange={draft.imageInlineConcurrency.change}
          />
        </div>
        <SwitchRow
          label={t("imageInlineSsrfGuard")}
          desc={t("imageInlineSsrfGuardDesc")}
          checked={draft.imageInlineSsrfGuard.value}
          onChange={draft.imageInlineSsrfGuard.change}
        />
        <div className="flex flex-col gap-1.5">
          <Label>{t("imageInlineHostAllowlist")}</Label>
          <p className="text-label text-muted-foreground">
            {t("imageInlineHostAllowlistDesc")}
          </p>
          <Input
            type="text"
            placeholder="example.com,*.internal.com"
            value={draft.imageInlineHostAllowlist.value}
            onChange={(event) =>
              draft.imageInlineHostAllowlist.change(event.target.value)
            }
            className="w-full max-w-md"
          />
        </div>
      </SettingsGroup>
    </SettingsCard>
  );
}

interface PolicyBillingSettingsDraft {
  minQuotaReserve: TextSettingInput;
  pricingPriority: TextSettingInput;
  pricingThreshold: TextSettingInput;
  rebuildSliceSleep: TextSettingInput;
  traceMaxBodyKB: { value: number; change: (value: number) => void };
  registrationEnabled: BooleanSettingInput;
  oauthAutoCreate: BooleanSettingInput;
  tokenModelWhitelistSelfService: BooleanSettingInput;
  inviteEnabled: BooleanSettingInput;
  inviteMaxCodes: TextSettingInput;
  inviteMaxUses: TextSettingInput;
}

function PolicyBillingSettingsContent({
  draft,
  saveAction,
  t,
}: {
  draft: PolicyBillingSettingsDraft;
  saveAction: SettingsSaveAction;
  t: SystemTranslator;
}) {
  return (
    <SettingsCard
      t={t}
      saveDisabled={saveAction.disabled}
      onSave={saveAction.run}
    >
      <SettingsGroup title={t("secQuotaGate")}>
        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          <NumField
            label={t("minQuotaReserve")}
            desc={t("minQuotaReserveDesc")}
            value={draft.minQuotaReserve.value}
            min={0}
            max={1000000000}
            unit="quota"
            humanizeAs="quota"
            onChange={draft.minQuotaReserve.change}
          />
        </div>
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("pricingSyncSettings")}>
        <div className="flex flex-col gap-1.5">
          <Label className="text-xs">{t("pricingSourcePriority")}</Label>
          <Input
            value={draft.pricingPriority.value}
            placeholder="models.dev,basellm"
            onChange={(event) =>
              draft.pricingPriority.change(event.target.value)
            }
            className="w-full max-w-md"
          />
        </div>
        <NumField
          label={t("pricingDisagreementThreshold")}
          desc={t("pricingDisagreementThresholdDesc")}
          value={draft.pricingThreshold.value}
          min={0}
          max={1}
          step={0.05}
          humanizeAs="ratio"
          onChange={draft.pricingThreshold.change}
        />
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secBillingRebuild")}>
        <NumField
          label={t("rebuildSliceSleep")}
          desc={t("rebuildSliceSleepDesc")}
          value={draft.rebuildSliceSleep.value}
          min={0}
          max={60000}
          unit="ms"
          humanizeAs="milliseconds"
          onChange={draft.rebuildSliceSleep.change}
        />
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secTrace")}>
        <div className="flex flex-col gap-1.5">
          <NumField
            label={t("traceMaxBodySize")}
            desc={t("traceMaxBodySizeDesc")}
            value={String(draft.traceMaxBodyKB.value)}
            min={4}
            max={16384}
            unit={t("traceMaxBodySizeUnit")}
            humanizeAs="kilobytes"
            onChange={(value) => draft.traceMaxBodyKB.change(Number(value))}
          />
          <p className="text-meta text-muted-foreground">
            {t("traceMaxBodySizeRange")}
          </p>
        </div>
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secRegistration")}>
        <SwitchRow
          label={t("registrationEnabled")}
          desc={t("registrationEnabledDesc")}
          checked={draft.registrationEnabled.value}
          onChange={draft.registrationEnabled.change}
        />
        <SwitchRow
          label={t("oauthAutoCreate")}
          desc={t("oauthAutoCreateDesc")}
          checked={draft.oauthAutoCreate.value}
          onChange={draft.oauthAutoCreate.change}
        />
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secTokenPermissions")}>
        <SwitchRow
          label={t("tokenModelWhitelistSelfService")}
          desc={t("tokenModelWhitelistSelfServiceDesc")}
          checked={draft.tokenModelWhitelistSelfService.value}
          onChange={draft.tokenModelWhitelistSelfService.change}
        />
      </SettingsGroup>
      <Separator />
      <SettingsGroup title={t("secInvite")}>
        <SwitchRow
          label={t("inviteEnabled")}
          desc={t("inviteEnabledDesc")}
          checked={draft.inviteEnabled.value}
          onChange={draft.inviteEnabled.change}
        />
        {draft.inviteEnabled.value && (
          <>
            <NumField
              label={t("inviteMaxCodes")}
              desc={t("inviteMaxCodesDesc")}
              value={draft.inviteMaxCodes.value}
              min={0}
              max={10000}
              onChange={draft.inviteMaxCodes.change}
            />
            <NumField
              label={t("inviteMaxUses")}
              desc={t("inviteMaxUsesDesc")}
              value={draft.inviteMaxUses.value}
              min={1}
              max={10000}
              onChange={draft.inviteMaxUses.change}
            />
          </>
        )}
      </SettingsGroup>
    </SettingsCard>
  );
}

function DatabaseStatsCard({
  tables,
  t,
}: {
  tables?: TableStats[];
  t: SystemTranslator;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database className="size-5" />
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
            {tables?.map((table) => (
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
  );
}

interface DataCleanupAction {
  target: string;
  retainDays: number;
  preview?: CleanupPreviewResponse;
  previewVisible: boolean;
  previewRefreshing: boolean;
  changeTarget: (value: string) => void;
  changeRetainDays: (value: number) => void;
  showPreview: () => void;
  requestConfirmation: () => void;
}

function DataCleanupCard({
  action,
  t,
}: {
  action: DataCleanupAction;
  t: SystemTranslator;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Trash2 className="size-5" />
          {t("dataCleanup")}
        </CardTitle>
        <CardDescription>{t("dataCleanupDesc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-end gap-4">
          <div className="space-y-2">
            <Label>{t("cleanupTarget")}</Label>
            <Select value={action.target} onValueChange={action.changeTarget}>
              <SelectTrigger className="w-[180px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="traces">{t("traceData")}</SelectItem>
                <SelectItem value="logs">{t("logData")}</SelectItem>
                <SelectItem value="hourly_buckets">
                  {t("hourlyBucketData")}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>{t("retainDays")}</Label>
            <Input
              type="number"
              min={1}
              value={action.retainDays}
              onChange={(event) =>
                action.changeRetainDays(Number(event.target.value))
              }
              className="w-[120px]"
            />
          </div>
          <Button variant="outline" onClick={action.showPreview}>
            <Activity data-icon="inline-start" />
            {t("preview")}
          </Button>
        </div>
        {action.target === "hourly_buckets" && (
          <p className="text-xs text-muted-foreground">
            {t("cleanupHourlyHint")}
          </p>
        )}
        {action.preview && action.previewVisible && (
          <div className="space-y-2 rounded-md border p-4">
            <p>
              {t("totalRecords")}:{" "}
              <span className="font-mono">
                {action.preview.total.toLocaleString()}
              </span>
            </p>
            <p>
              {t("toDelete")}:{" "}
              <span className="font-mono text-destructive">
                {action.preview.to_delete.toLocaleString()}
              </span>
            </p>
            <Button
              variant="destructive"
              disabled={
                action.preview.to_delete === 0 || action.previewRefreshing
              }
              onClick={action.requestConfirmation}
            >
              <Trash2 data-icon="inline-start" />
              {t("executeCleanup")}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
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
  const [fallbackSleepInput, setFallbackSleepInput] = useState<string | null>(
    null,
  );
  const [maxRetriesPerChannelInput, setMaxRetriesPerChannelInput] = useState<
    string | null
  >(null);
  const [retryMaxChannelsInput, setRetryMaxChannelsInput] = useState<
    string | null
  >(null);
  const [retryBackoffBaseInput, setRetryBackoffBaseInput] = useState<
    string | null
  >(null);
  const [retryBackoffMaxInput, setRetryBackoffMaxInput] = useState<
    string | null
  >(null);
  const [breakerThresholdInput, setBreakerThresholdInput] = useState<
    string | null
  >(null);
  const [breakerCooldownInput, setBreakerCooldownInput] = useState<
    string | null
  >(null);
  const [breakerEnabledInput, setBreakerEnabledInput] = useState<
    boolean | null
  >(null);
  const [minQuotaReserveInput, setMinQuotaReserveInput] = useState<
    string | null
  >(null);
  const [rateLimiterEnabledInput, setRateLimiterEnabledInput] = useState<
    boolean | null
  >(null);
  const [sseKeepaliveInput, setSseKeepaliveInput] = useState<string | null>(
    null,
  );
  const [queueTimeInput, setQueueTimeInput] = useState<string | null>(null);
  const [
    tokenModelWhitelistSelfServiceInput,
    setTokenModelWhitelistSelfServiceInput,
  ] = useState<boolean | null>(null);
  const [cleanupTarget, setCleanupTarget] = useState("traces");
  const [retainDays, setRetainDays] = useState(30);
  const [showPreview, setShowPreview] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const {
    data: preview,
    dataUpdatedAt: previewFetchedAt,
    isFetching: previewRefreshing,
    refetch: refetchCleanupPreview,
  } = useCleanupPreview(cleanupTarget, retainDays, showPreview);

  const currentTraceKB = settings?.settings?.trace_max_body_size
    ? Math.round(Number(settings.settings.trace_max_body_size) / 1024)
    : 64;
  const displayKB = traceMaxBodyKB ?? currentTraceKB;
  const traceHasChanges = displayKB !== currentTraceKB;

  const currentProxyUrl = settings?.settings?.proxy_url ?? "";
  const displayProxyUrl = proxyUrlInput ?? currentProxyUrl;
  const proxyHasChanges = displayProxyUrl !== currentProxyUrl;

  const [pricingPriorityInput, setPricingPriorityInput] = useState<
    string | null
  >(null);
  const [pricingThresholdInput, setPricingThresholdInput] = useState<
    string | null
  >(null);
  const [rebuildSliceSleepInput, setRebuildSliceSleepInput] = useState<
    string | null
  >(null);
  const currentPricingPriority =
    settings?.settings?.pricing_source_priority ?? "models.dev,basellm";
  const currentPricingThreshold =
    settings?.settings?.pricing_disagreement_threshold ?? "0.2";
  const displayPricingPriority = pricingPriorityInput ?? currentPricingPriority;
  const displayPricingThreshold =
    pricingThresholdInput ?? currentPricingThreshold;
  const pricingPriorityHasChanges =
    displayPricingPriority !== currentPricingPriority;
  const pricingThresholdHasChanges =
    displayPricingThreshold !== currentPricingThreshold;

  const currentRebuildSliceSleep = settings?.settings?.[
    "billing.rebuild_slice_sleep_ms"
  ]
    ? Number(settings.settings["billing.rebuild_slice_sleep_ms"])
    : 1000;
  const displayRebuildSliceSleep =
    rebuildSliceSleepInput ?? String(currentRebuildSliceSleep);
  const rebuildSliceSleepHasChanges =
    displayRebuildSliceSleep !== String(currentRebuildSliceSleep);

  const currentRegistrationEnabled =
    settings?.settings?.registration_enabled === "true";
  const [registrationInput, setRegistrationInput] = useState<boolean | null>(
    null,
  );
  const displayRegistrationEnabled =
    registrationInput ?? currentRegistrationEnabled;
  const registrationHasChanges =
    displayRegistrationEnabled !== currentRegistrationEnabled;

  const currentAffinityEnabled = settings?.settings?.affinity_enabled === "1";
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
  const displayFallbackSleep =
    fallbackSleepInput ?? String(currentFallbackSleepMs);
  const fallbackSleepHasChanges =
    displayFallbackSleep !== String(currentFallbackSleepMs);

  const currentMaxRetriesPerChannel = settings?.settings
    ?.max_retries_per_channel
    ? Number(settings.settings.max_retries_per_channel)
    : 2;
  const displayMaxRetriesPerChannel =
    maxRetriesPerChannelInput ?? String(currentMaxRetriesPerChannel);
  const maxRetriesPerChannelHasChanges =
    displayMaxRetriesPerChannel !== String(currentMaxRetriesPerChannel);

  const currentRetryMaxChannels = settings?.settings?.retry_max_channels
    ? Number(settings.settings.retry_max_channels)
    : 5;
  const displayRetryMaxChannels =
    retryMaxChannelsInput ?? String(currentRetryMaxChannels);
  const retryMaxChannelsHasChanges =
    displayRetryMaxChannels !== String(currentRetryMaxChannels);

  const currentRetryBackoffBase = settings?.settings?.retry_backoff_base_ms
    ? Number(settings.settings.retry_backoff_base_ms)
    : 200;
  const displayRetryBackoffBase =
    retryBackoffBaseInput ?? String(currentRetryBackoffBase);
  const retryBackoffBaseHasChanges =
    displayRetryBackoffBase !== String(currentRetryBackoffBase);

  const currentRetryBackoffMax = settings?.settings?.retry_backoff_max_ms
    ? Number(settings.settings.retry_backoff_max_ms)
    : 2000;
  const displayRetryBackoffMax =
    retryBackoffMaxInput ?? String(currentRetryBackoffMax);
  const retryBackoffMaxHasChanges =
    displayRetryBackoffMax !== String(currentRetryBackoffMax);

  const currentBreakerThreshold = settings?.settings?.breaker_threshold
    ? Number(settings.settings.breaker_threshold)
    : 5;
  const displayBreakerThreshold =
    breakerThresholdInput ?? String(currentBreakerThreshold);
  const breakerThresholdHasChanges =
    displayBreakerThreshold !== String(currentBreakerThreshold);

  const currentBreakerCooldown = settings?.settings?.breaker_cooldown_ms
    ? Number(settings.settings.breaker_cooldown_ms)
    : 30000;
  const displayBreakerCooldown =
    breakerCooldownInput ?? String(currentBreakerCooldown);
  const breakerCooldownHasChanges =
    displayBreakerCooldown !== String(currentBreakerCooldown);

  const currentBreakerEnabled = settings?.settings?.breaker_enabled !== "0";
  const displayBreakerEnabled = breakerEnabledInput ?? currentBreakerEnabled;
  const breakerEnabledHasChanges =
    displayBreakerEnabled !== currentBreakerEnabled;

  const currentMinQuotaReserve = settings?.settings?.min_quota_reserve
    ? Number(settings.settings.min_quota_reserve)
    : 0;
  const displayMinQuotaReserve =
    minQuotaReserveInput ?? String(currentMinQuotaReserve);
  const minQuotaReserveHasChanges =
    displayMinQuotaReserve !== String(currentMinQuotaReserve);

  // 请求级限流的三项全局设置。rate_limiter_enabled 后端存 "0"/"1"（默认 1）。
  const currentRateLimiterEnabled =
    settings?.settings?.rate_limiter_enabled !== "0";
  const displayRateLimiterEnabled =
    rateLimiterEnabledInput ?? currentRateLimiterEnabled;
  const rateLimiterEnabledHasChanges =
    displayRateLimiterEnabled !== currentRateLimiterEnabled;

  const currentSseKeepalive = settings?.settings?.sse_keepalive_ms
    ? Number(settings.settings.sse_keepalive_ms)
    : 15000;
  const displaySseKeepalive = sseKeepaliveInput ?? String(currentSseKeepalive);
  const sseKeepaliveHasChanges =
    displaySseKeepalive !== String(currentSseKeepalive);

  const currentQueueTime = settings?.settings?.queue_time_ms
    ? Number(settings.settings.queue_time_ms)
    : 120000;
  const displayQueueTime = queueTimeInput ?? String(currentQueueTime);
  const queueTimeHasChanges = displayQueueTime !== String(currentQueueTime);

  const currentTokenModelWhitelistSelfService =
    settings?.settings?.token_model_whitelist_self_service === "true";
  const displayTokenModelWhitelistSelfService =
    tokenModelWhitelistSelfServiceInput ??
    currentTokenModelWhitelistSelfService;
  const tokenModelWhitelistSelfServiceHasChanges =
    displayTokenModelWhitelistSelfService !==
    currentTokenModelWhitelistSelfService;

  const currentAutoCreate = settings?.settings?.oauth_auto_create === "true";
  const [autoCreateInput, setAutoCreateInput] = useState<boolean | null>(null);
  const displayAutoCreate = autoCreateInput ?? currentAutoCreate;
  const autoCreateHasChanges = displayAutoCreate !== currentAutoCreate;

  const currentInviteEnabled = settings?.settings?.invite_enabled === "true";
  const [inviteEnabledInput, setInviteEnabledInput] = useState<boolean | null>(
    null,
  );
  const displayInviteEnabled = inviteEnabledInput ?? currentInviteEnabled;
  const inviteEnabledHasChanges = displayInviteEnabled !== currentInviteEnabled;

  const currentInviteMaxCodes =
    settings?.settings?.invite_user_max_codes ?? "5";
  const [inviteMaxCodesInput, setInviteMaxCodesInput] = useState<string | null>(
    null,
  );
  const displayInviteMaxCodes = inviteMaxCodesInput ?? currentInviteMaxCodes;
  const inviteMaxCodesHasChanges =
    displayInviteMaxCodes !== currentInviteMaxCodes;

  const currentInviteMaxUses = settings?.settings?.invite_user_max_uses ?? "1";
  const [inviteMaxUsesInput, setInviteMaxUsesInput] = useState<string | null>(
    null,
  );
  const displayInviteMaxUses = inviteMaxUsesInput ?? currentInviteMaxUses;
  const inviteMaxUsesHasChanges = displayInviteMaxUses !== currentInviteMaxUses;

  // 图片内联抓取(image inline fetch)设置。ssrf guard 默认开启("1"),存 "1"/"0" 字符串。
  const [imageInlineFetchTimeoutSecInput, setImageInlineFetchTimeoutSecInput] =
    useState<string | null>(null);
  const [imageInlineMaxBytesInput, setImageInlineMaxBytesInput] = useState<
    string | null
  >(null);
  const [imageInlineConcurrencyInput, setImageInlineConcurrencyInput] =
    useState<string | null>(null);
  const [imageInlineSsrfGuardInput, setImageInlineSsrfGuardInput] = useState<
    boolean | null
  >(null);
  const [imageInlineHostAllowlistInput, setImageInlineHostAllowlistInput] =
    useState<string | null>(null);

  const currentImageInlineFetchTimeoutSec = settings?.settings
    ?.image_inline_fetch_timeout_sec
    ? Number(settings.settings.image_inline_fetch_timeout_sec)
    : 10;
  const displayImageInlineFetchTimeoutSec =
    imageInlineFetchTimeoutSecInput ??
    String(currentImageInlineFetchTimeoutSec);
  const imageInlineFetchTimeoutSecHasChanges =
    displayImageInlineFetchTimeoutSec !==
    String(currentImageInlineFetchTimeoutSec);

  const currentImageInlineMaxBytes = settings?.settings?.image_inline_max_bytes
    ? Number(settings.settings.image_inline_max_bytes)
    : 10485760;
  const displayImageInlineMaxBytes =
    imageInlineMaxBytesInput ?? String(currentImageInlineMaxBytes);
  const imageInlineMaxBytesHasChanges =
    displayImageInlineMaxBytes !== String(currentImageInlineMaxBytes);

  const currentImageInlineConcurrency = settings?.settings
    ?.image_inline_concurrency
    ? Number(settings.settings.image_inline_concurrency)
    : 4;
  const displayImageInlineConcurrency =
    imageInlineConcurrencyInput ?? String(currentImageInlineConcurrency);
  const imageInlineConcurrencyHasChanges =
    displayImageInlineConcurrency !== String(currentImageInlineConcurrency);

  const currentImageInlineSsrfGuard =
    settings?.settings?.image_inline_ssrf_guard !== "0";
  const displayImageInlineSsrfGuard =
    imageInlineSsrfGuardInput ?? currentImageInlineSsrfGuard;
  const imageInlineSsrfGuardHasChanges =
    displayImageInlineSsrfGuard !== currentImageInlineSsrfGuard;

  const currentImageInlineHostAllowlist =
    settings?.settings?.image_inline_host_allowlist ?? "";
  const displayImageInlineHostAllowlist =
    imageInlineHostAllowlistInput ?? currentImageInlineHostAllowlist;
  const imageInlineHostAllowlistHasChanges =
    displayImageInlineHostAllowlist !== currentImageInlineHostAllowlist;

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
    tokenModelWhitelistSelfServiceHasChanges ||
    pricingPriorityHasChanges ||
    pricingThresholdHasChanges ||
    rebuildSliceSleepHasChanges ||
    imageInlineFetchTimeoutSecHasChanges ||
    imageInlineMaxBytesHasChanges ||
    imageInlineConcurrencyHasChanges ||
    imageInlineSsrfGuardHasChanges ||
    imageInlineHostAllowlistHasChanges;

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
    if (tokenModelWhitelistSelfServiceHasChanges) {
      updates.token_model_whitelist_self_service = String(
        displayTokenModelWhitelistSelfService,
      );
    }
    if (affinityHasChanges) {
      updates.affinity_enabled = displayAffinityEnabled ? "1" : "0";
      updates.affinity_ttl_sec = String(
        parseInt(displayAffinityTTL, 10) || 300,
      );
    }
    if (pricingPriorityHasChanges) {
      updates.pricing_source_priority = displayPricingPriority;
    }
    if (pricingThresholdHasChanges) {
      updates.pricing_disagreement_threshold = displayPricingThreshold;
    }
    if (rebuildSliceSleepHasChanges) {
      const n = Number(rebuildSliceSleepInput);
      if (!Number.isFinite(n) || n < 0 || n > 60000) {
        toast.error(t("rebuildSliceSleepRangeError"));
        return;
      }
      updates["billing.rebuild_slice_sleep_ms"] = String(n);
    }
    if (imageInlineFetchTimeoutSecHasChanges) {
      updates.image_inline_fetch_timeout_sec = String(
        displayImageInlineFetchTimeoutSec,
      );
    }
    if (imageInlineMaxBytesHasChanges) {
      updates.image_inline_max_bytes = String(displayImageInlineMaxBytes);
    }
    if (imageInlineConcurrencyHasChanges) {
      updates.image_inline_concurrency = String(displayImageInlineConcurrency);
    }
    if (imageInlineSsrfGuardHasChanges) {
      updates.image_inline_ssrf_guard = displayImageInlineSsrfGuard ? "1" : "0";
    }
    if (imageInlineHostAllowlistHasChanges) {
      updates.image_inline_host_allowlist = displayImageInlineHostAllowlist;
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
          setTokenModelWhitelistSelfServiceInput(null);
          setPricingPriorityInput(null);
          setPricingThresholdInput(null);
          setRebuildSliceSleepInput(null);
          setImageInlineFetchTimeoutSecInput(null);
          setImageInlineMaxBytesInput(null);
          setImageInlineConcurrencyInput(null);
          setImageInlineSsrfGuardInput(null);
          setImageInlineHostAllowlistInput(null);
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
    if (!preview) return;
    if (
      isCleanupPreviewExpired(preview.cutoff_unix, retainDays, previewFetchedAt)
    ) {
      setConfirmOpen(false);
      toast.error(t("cleanupPreviewExpired"));
      void refetchCleanupPreview();
      return;
    }
    cleanup.mutate(
      {
        target: cleanupTarget,
        retain_days: retainDays,
        cutoff_unix: preview.cutoff_unix,
      },
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

  const saveAction: SettingsSaveAction = {
    disabled: !hasChanges || updateSettings.isPending,
    run: handleSaveSettings,
  };
  const requestPathDraft: RequestPathSettingsDraft = {
    fallbackSleep: {
      value: displayFallbackSleep,
      change: setFallbackSleepInput,
    },
    maxRetriesPerChannel: {
      value: displayMaxRetriesPerChannel,
      change: setMaxRetriesPerChannelInput,
    },
    retryMaxChannels: {
      value: displayRetryMaxChannels,
      change: setRetryMaxChannelsInput,
    },
    retryBackoffBase: {
      value: displayRetryBackoffBase,
      change: setRetryBackoffBaseInput,
    },
    retryBackoffMax: {
      value: displayRetryBackoffMax,
      change: setRetryBackoffMaxInput,
    },
    breakerEnabled: {
      value: displayBreakerEnabled,
      change: setBreakerEnabledInput,
    },
    breakerThreshold: {
      value: displayBreakerThreshold,
      change: setBreakerThresholdInput,
    },
    breakerCooldown: {
      value: displayBreakerCooldown,
      change: setBreakerCooldownInput,
    },
    rateLimiterEnabled: {
      value: displayRateLimiterEnabled,
      change: setRateLimiterEnabledInput,
    },
    sseKeepalive: { value: displaySseKeepalive, change: setSseKeepaliveInput },
    queueTime: { value: displayQueueTime, change: setQueueTimeInput },
    affinityEnabled: {
      value: displayAffinityEnabled,
      change: setAffinityInput,
    },
    affinityTTL: { value: displayAffinityTTL, change: setAffinityTTLInput },
    proxyUrl: { value: displayProxyUrl, change: setProxyUrlInput },
    imageInlineFetchTimeoutSec: {
      value: displayImageInlineFetchTimeoutSec,
      change: setImageInlineFetchTimeoutSecInput,
    },
    imageInlineMaxBytes: {
      value: displayImageInlineMaxBytes,
      change: setImageInlineMaxBytesInput,
    },
    imageInlineConcurrency: {
      value: displayImageInlineConcurrency,
      change: setImageInlineConcurrencyInput,
    },
    imageInlineSsrfGuard: {
      value: displayImageInlineSsrfGuard,
      change: setImageInlineSsrfGuardInput,
    },
    imageInlineHostAllowlist: {
      value: displayImageInlineHostAllowlist,
      change: setImageInlineHostAllowlistInput,
    },
  };
  const policyBillingDraft: PolicyBillingSettingsDraft = {
    minQuotaReserve: {
      value: displayMinQuotaReserve,
      change: setMinQuotaReserveInput,
    },
    pricingPriority: {
      value: displayPricingPriority,
      change: setPricingPriorityInput,
    },
    pricingThreshold: {
      value: displayPricingThreshold,
      change: setPricingThresholdInput,
    },
    rebuildSliceSleep: {
      value: displayRebuildSliceSleep,
      change: setRebuildSliceSleepInput,
    },
    traceMaxBodyKB: { value: displayKB, change: setTraceMaxBodyKB },
    registrationEnabled: {
      value: displayRegistrationEnabled,
      change: setRegistrationInput,
    },
    oauthAutoCreate: { value: displayAutoCreate, change: setAutoCreateInput },
    tokenModelWhitelistSelfService: {
      value: displayTokenModelWhitelistSelfService,
      change: setTokenModelWhitelistSelfServiceInput,
    },
    inviteEnabled: {
      value: displayInviteEnabled,
      change: setInviteEnabledInput,
    },
    inviteMaxCodes: {
      value: displayInviteMaxCodes,
      change: setInviteMaxCodesInput,
    },
    inviteMaxUses: {
      value: displayInviteMaxUses,
      change: setInviteMaxUsesInput,
    },
  };
  const cleanupAction: DataCleanupAction = {
    target: cleanupTarget,
    retainDays,
    preview,
    previewVisible: showPreview,
    previewRefreshing,
    changeTarget: (value) => {
      setCleanupTarget(value);
      setShowPreview(false);
    },
    changeRetainDays: (value) => {
      setRetainDays(value);
      setShowPreview(false);
    },
    showPreview: handlePreview,
    requestConfirmation: () => setConfirmOpen(true),
  };

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <Button variant="outline" size="sm" onClick={() => refetch()}>
          <RefreshCw
            data-icon="inline-start"
            className={isLoading ? "animate-spin" : undefined}
          />
          {t("refresh")}
        </Button>
      </div>

      <SystemMaintenanceTabs
        overview={<SystemInfoCard system={stats?.system} t={t} />}
        requestPath={
          <RequestPathSettingsContent
            draft={requestPathDraft}
            saveAction={saveAction}
            t={t}
          />
        }
        policyBilling={
          <PolicyBillingSettingsContent
            draft={policyBillingDraft}
            saveAction={saveAction}
            t={t}
          />
        }
        byok={<BYOKSettingsCard />}
        dataMaintenance={
          <div className="flex min-w-0 flex-col gap-6">
            <LogStorageStatus storage={stats?.storage} />
            <DatabaseStatsCard tables={stats?.tables} t={t} />
            <DataCleanupCard action={cleanupAction} t={t} />
          </div>
        }
      />

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
