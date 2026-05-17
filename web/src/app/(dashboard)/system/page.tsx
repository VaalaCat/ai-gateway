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

export default function SystemMaintenancePage() {
  const t = useTranslations("system");
  const { data: stats, refetch, isLoading } = useSystemStats();
  const cleanup = useCleanup();
  const { data: settings } = useSettings();
  const updateSettings = useUpdateSettings();

  const [traceMaxBodyKB, setTraceMaxBodyKB] = useState<number | null>(null);
  const [proxyUrlInput, setProxyUrlInput] = useState<string | null>(null);
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

  const currentRegistrationEnabled =
    settings?.settings?.registration_enabled === "true";
  const [registrationInput, setRegistrationInput] = useState<boolean | null>(
    null,
  );
  const displayRegistrationEnabled =
    registrationInput ?? currentRegistrationEnabled;
  const registrationHasChanges =
    displayRegistrationEnabled !== currentRegistrationEnabled;

  const currentAutoCreate = settings?.settings?.oauth_auto_create === "true";
  const [autoCreateInput, setAutoCreateInput] = useState<boolean | null>(null);
  const displayAutoCreate = autoCreateInput ?? currentAutoCreate;
  const autoCreateHasChanges = displayAutoCreate !== currentAutoCreate;

  const hasChanges =
    traceHasChanges ||
    proxyHasChanges ||
    registrationHasChanges ||
    autoCreateHasChanges;

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

  const formatBytes = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024)
      return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  };

  const formatUptime = (seconds: number) => {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    if (days > 0) return `${days}d ${hours}h ${mins}m`;
    if (hours > 0) return `${hours}h ${mins}m`;
    return `${mins}m`;
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
                <p className="text-sm text-muted-foreground">{t("version")}</p>
                <p className="font-mono">{stats.system.version}</p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">
                  {t("goVersion")}
                </p>
                <p className="font-mono">{stats.system.go_version}</p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">{t("uptime")}</p>
                <p className="font-mono">
                  {formatUptime(stats.system.uptime_sec)}
                </p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">
                  {t("onlineAgents")}
                </p>
                <p className="font-mono">{stats.system.online_agents}</p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">
                  {t("memoryAlloc")}
                </p>
                <p className="font-mono">
                  {formatBytes(stats.system.memory_alloc)}
                </p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">
                  {t("memorySys")}
                </p>
                <p className="font-mono">
                  {formatBytes(stats.system.memory_sys)}
                </p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">{t("gcCount")}</p>
                <p className="font-mono">{stats.system.num_gc}</p>
              </div>
              <div>
                <p className="text-sm text-muted-foreground">
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
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label>{t("traceMaxBodySize")}</Label>
            <p className="text-sm text-muted-foreground">
              {t("traceMaxBodySizeDesc")}
            </p>
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={4}
                max={16384}
                value={displayKB}
                onChange={(e) => setTraceMaxBodyKB(Number(e.target.value))}
                className="w-[150px]"
              />
              <span className="text-sm text-muted-foreground">
                {t("traceMaxBodySizeUnit")}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              {t("traceMaxBodySizeRange")}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t("proxyUrl")}</Label>
            <p className="text-sm text-muted-foreground">
              {t("proxyUrlDesc")}
            </p>
            <Input
              type="text"
              placeholder={t("proxyUrlPlaceholder")}
              value={displayProxyUrl}
              onChange={(e) => setProxyUrlInput(e.target.value)}
              className="max-w-md"
            />
          </div>
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-1">
              <Label>{t("registrationEnabled")}</Label>
              <p className="text-sm text-muted-foreground">
                {t("registrationEnabledDesc")}
              </p>
            </div>
            <Switch
              checked={displayRegistrationEnabled}
              onCheckedChange={(v) => setRegistrationInput(v)}
            />
          </div>
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-1">
              <Label>{t("oauthAutoCreate")}</Label>
              <p className="text-sm text-muted-foreground">
                {t("oauthAutoCreateDesc")}
              </p>
            </div>
            <Switch
              checked={displayAutoCreate}
              onCheckedChange={(v) => setAutoCreateInput(v)}
            />
          </div>
          <Button
            onClick={handleSaveSettings}
            disabled={!hasChanges || updateSettings.isPending}
          >
            {t("saveSettings")}
          </Button>
        </CardContent>
      </Card>

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
                  cleanupTarget === "traces" ? t("traceData") : t("logData"),
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
