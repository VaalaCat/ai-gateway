'use client';
import { useEffect, useState } from 'react';
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Plus, X, KeyRound } from 'lucide-react';
import { toast } from 'sonner';
import { useSettings, useUpdateSettings } from '@/lib/api/system';
import { useBYOKSystemBaseURLs, useBaseURLUsage } from '@/lib/api/byok-system-baseurls';
import { useTranslations } from 'next-intl';

export function BYOKSettingsCard() {
  const t = useTranslations('byok.settings');
  const tCommon = useTranslations('common');
  const { data: settingsResp } = useSettings();
  const { data: systemBaseURLsResp } = useBYOKSystemBaseURLs();
  const updateMut = useUpdateSettings();

  const [enabled, setEnabled] = useState(true);
  const [maxChannels, setMaxChannels] = useState<number>(20);
  const [billingMode, setBillingMode] = useState<'free' | 'service_fee'>('free');
  const [feeRatio, setFeeRatio] = useState<number>(0.1);
  const [extraURLs, setExtraURLs] = useState<string[]>([]);
  const [draftURL, setDraftURL] = useState('');

  // pendingDeleteURL drives the AlertDialog: setting it non-null opens the
  // dialog and triggers a usage lookup (useBaseURLUsage is enabled when prefix
  // is non-null). The dialog stays open until the admin clicks Confirm
  // (which removes the URL from local state — actual persistence still
  // requires clicking the outer "Save BYOK Settings" button) or Cancel.
  const [pendingDeleteURL, setPendingDeleteURL] = useState<string | null>(null);
  const usage = useBaseURLUsage(pendingDeleteURL);

  // Hydrate from server once
  useEffect(() => {
    if (!settingsResp) return;
    const s = settingsResp.settings;
    setEnabled((s.byok_enabled ?? 'true') === 'true');
    const mc = parseInt(s.byok_max_channels_per_user ?? '20', 10);
    setMaxChannels(isNaN(mc) ? 20 : mc);
    const mode = (s.byok_billing_mode ?? 'free') as 'free' | 'service_fee';
    setBillingMode(mode === 'service_fee' ? 'service_fee' : 'free');
    const fr = parseFloat(s.byok_service_fee_ratio ?? '0.1');
    setFeeRatio(isNaN(fr) ? 0.1 : fr);
    try {
      const parsed = JSON.parse(s.byok_base_url_allowlist ?? '[]');
      setExtraURLs(Array.isArray(parsed) ? parsed : []);
    } catch {
      setExtraURLs([]);
    }
  }, [settingsResp]);

  const systemURLs = systemBaseURLsResp?.urls ?? [];

  const handleSave = async () => {
    try {
      await updateMut.mutateAsync({
        settings: {
          byok_enabled: enabled ? 'true' : 'false',
          byok_max_channels_per_user: String(maxChannels),
          byok_billing_mode: billingMode,
          byok_service_fee_ratio: String(feeRatio),
          byok_base_url_allowlist: JSON.stringify(extraURLs),
        },
      });
      toast.success(t('savedToast'));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('saveFailedToast'));
    }
  };

  const addURL = () => {
    const u = draftURL.trim();
    if (!u) return;
    if (extraURLs.includes(u)) {
      toast.error(t('duplicateToast'));
      return;
    }
    setExtraURLs([...extraURLs, u]);
    setDraftURL('');
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="h-5 w-5" />
          {t('title')}
        </CardTitle>
        <CardDescription>
          {t('description')}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="flex items-center justify-between">
          <div className="space-y-1">
            <Label>{t('globallyEnabled')}</Label>
            <div className="text-xs text-muted-foreground">
              {t('globallyEnabledDesc')}
            </div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} />
        </div>

        <div className="space-y-1">
          <Label>{t('maxChannels')}</Label>
          <Input
            type="number"
            min={1}
            value={maxChannels}
            onChange={(e) => setMaxChannels(Number(e.target.value))}
            className="w-40"
          />
          <div className="text-xs text-muted-foreground">
            {t('maxChannelsDesc')}
          </div>
        </div>

        <div className="space-y-1">
          <Label>{t('billingMode')}</Label>
          <Select value={billingMode} onValueChange={(v) => setBillingMode(v as 'free' | 'service_fee')}>
            <SelectTrigger className="w-48"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="free">{t('billingFree')}</SelectItem>
              <SelectItem value="service_fee">{t('billingServiceFee')}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {billingMode === 'service_fee' && (
          <div className="space-y-1">
            <Label>{t('feeRatio')}</Label>
            <Input
              type="number"
              step="0.01"
              min={0}
              max={1}
              value={feeRatio}
              onChange={(e) => setFeeRatio(Number(e.target.value))}
              className="w-40"
            />
            <div className="text-xs text-muted-foreground">
              {t('feeRatioDesc')}
            </div>
          </div>
        )}

        <div className="space-y-3">
          <Label>{t('allowlist')}</Label>
          <div className="rounded-md border p-3 space-y-2">
            <div className="text-xs font-medium text-muted-foreground">{t('systemRecommended')}</div>
            <div className="flex flex-wrap gap-2">
              {systemURLs.length === 0 && (
                <div className="text-xs text-muted-foreground">{t('systemNone')}</div>
              )}
              {systemURLs.map((u) => (
                <Badge key={u} variant="secondary" className="font-mono">{u}</Badge>
              ))}
            </div>
          </div>
          <div className="rounded-md border p-3 space-y-3">
            <div className="text-xs font-medium text-muted-foreground">{t('customAdditions')}</div>
            {extraURLs.length === 0 && (
              <div className="text-xs text-muted-foreground">{t('customNone')}</div>
            )}
            {extraURLs.map((u, i) => (
              <div key={`${u}-${i}`} className="flex items-center gap-2">
                <code className="flex-1 text-sm font-mono break-all">{u}</code>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => setPendingDeleteURL(u)}
                  aria-label={t('deleteBaseURLTitle')}
                >
                  <X className="h-4 w-4" />
                </Button>
              </div>
            ))}
            <div className="flex gap-2">
              <Input
                placeholder="https://..."
                value={draftURL}
                onChange={(e) => setDraftURL(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    addURL();
                  }
                }}
              />
              <Button type="button" onClick={addURL}>
                <Plus className="h-4 w-4 mr-1" />{t('add')}
              </Button>
            </div>
          </div>
        </div>

        <div className="flex justify-end">
          <Button onClick={handleSave} disabled={updateMut.isPending}>
            {t('saveButton')}
          </Button>
        </div>
      </CardContent>

      <AlertDialog
        open={!!pendingDeleteURL}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteURL(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('deleteBaseURLTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {usage.isLoading
                ? t('deleteBaseURLChecking')
                : usage.data && usage.data.count > 0
                ? t('deleteBaseURLWarning', { count: usage.data.count })
                : t('deleteBaseURLSafe')}
            </AlertDialogDescription>
          </AlertDialogHeader>

          {pendingDeleteURL && (
            <div className="rounded-md border bg-muted/30 p-2">
              <code className="text-sm font-mono break-all">{pendingDeleteURL}</code>
            </div>
          )}

          {usage.data && usage.data.channels.length > 0 && (
            <div className="max-h-60 overflow-auto rounded-md border">
              <Table className="text-body">
                <TableHeader className="bg-muted/40 text-xs uppercase">
                  <TableRow>
                    <TableHead className="px-3 py-2 font-medium">
                      {t('deleteBaseURLColOwner')}
                    </TableHead>
                    <TableHead className="px-3 py-2 font-medium">
                      {t('deleteBaseURLColChannel')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {usage.data.channels.map((row, idx) => (
                    <TableRow key={`${row.owner_id}-${row.channel_name}-${idx}`}>
                      <TableCell className="px-3 py-1.5 font-mono">{row.owner_id}</TableCell>
                      <TableCell className="px-3 py-1.5">{row.channel_name}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {usage.data.count > usage.data.channels.length && (
                <div className="border-t bg-muted/20 px-3 py-1.5 text-xs text-muted-foreground">
                  {t('deleteBaseURLTruncated', {
                    shown: usage.data.channels.length,
                    total: usage.data.count,
                  })}
                </div>
              )}
            </div>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel>{tCommon('cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pendingDeleteURL) {
                  setExtraURLs(extraURLs.filter((u) => u !== pendingDeleteURL));
                }
                setPendingDeleteURL(null);
              }}
            >
              {t('deleteBaseURLConfirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
