"use client";

import { useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { AgentAddressEditor } from "@/components/business/agent-address-editor";
import {
  AgentRelayConfigFields,
  validateRelayURI,
} from "@/components/business/agent-relay-config-fields";
import { StatusSelect } from "@/components/business/status-select";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLegend,
  FieldSet,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useAgentDetail, useUpdateAgent } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { AgentDetail, AgentPatch, PeerRouteMode, RelayMode } from "@/lib/types";

interface EditForm {
  name: string;
  status: string;
  tags: string;
  http_addresses: string;
  proxy_url: string;
  relay_mode: RelayMode;
  relay_uri: string;
  peer_route_mode: PeerRouteMode;
}

function formFromDetail(detail: AgentDetail): EditForm {
  return {
    name: detail.name,
    status: String(detail.status),
    tags: detail.tags,
    http_addresses: detail.configured_http_addresses ?? detail.http_addresses,
    proxy_url: detail.proxy_url,
    relay_mode: detail.relay_mode,
    relay_uri: detail.relay_uri,
    peer_route_mode: detail.peer_route_mode,
  };
}

function buildAgentPatch(detail: AgentDetail, form: EditForm): AgentPatch {
  const patch: AgentPatch = {};
  const values = {
    name: form.name,
    status: Number(form.status),
    tags: form.tags,
    http_addresses: form.http_addresses,
    proxy_url: form.proxy_url,
    relay_mode: form.relay_mode,
    relay_uri: form.relay_uri,
    peer_route_mode: form.peer_route_mode,
  };
  const baseline = {
    name: detail.name,
    status: detail.status,
    tags: detail.tags,
    http_addresses: detail.configured_http_addresses ?? detail.http_addresses,
    proxy_url: detail.proxy_url,
    relay_mode: detail.relay_mode,
    relay_uri: detail.relay_uri,
    peer_route_mode: detail.peer_route_mode,
  };
  for (const key of Object.keys(values) as Array<keyof typeof values>) {
    if (values[key] !== baseline[key]) {
      Object.assign(patch, { [key]: values[key] });
    }
  }
  return patch;
}

interface AgentEditDialogProps {
  open: boolean;
  agentId: number | null;
  onOpenChange: (open: boolean) => void;
}

export function AgentEditDialog({ open, agentId, onOpenChange }: AgentEditDialogProps) {
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const detail = useAgentDetail(agentId ?? 0, { enabled: open && Boolean(agentId) });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{tc("edit")}</DialogTitle>
          <DialogDescription className="sr-only">{t("editDescription")}</DialogDescription>
        </DialogHeader>

        {!detail.data && detail.error ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
              <span>{t("editLoadFailed")}</span>
              <Button type="button" variant="outline" size="sm" onClick={() => void detail.refetch()}>{t("retry")}</Button>
            </AlertDescription>
          </Alert>
        ) : detail.isLoading || !detail.data || !agentId ? (
          <div aria-label={t("loadingEdit")} className="flex flex-col gap-3 py-2">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : (
          <LoadedAgentEditor
            key={agentId}
            agentId={agentId}
            initialDetail={detail.data}
            refreshError={detail.error}
            onRetry={() => void detail.refetch()}
            onClose={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function LoadedAgentEditor({ agentId, initialDetail, refreshError, onRetry, onClose }: {
  agentId: number;
  initialDetail: AgentDetail;
  refreshError: Error | null;
  onRetry: () => void;
  onClose: () => void;
}) {
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const update = useUpdateAgent();
  const [baseline] = useState(initialDetail);
  const [form, setForm] = useState(() => formFromDetail(initialDetail));
  const [submitError, setSubmitError] = useState("");
  const relayValidation = form.relay_mode === "custom"
    ? validateRelayURI(form.relay_uri)
    : undefined;
  const patch = buildAgentPatch(baseline, form);
  const hasChanges = Object.keys(patch).length > 0;

  const save = async () => {
    if (relayValidation && "error" in relayValidation) return;
    const submittedPatch = { ...patch };
    if (
      submittedPatch.relay_uri !== undefined &&
      relayValidation &&
      "normalized" in relayValidation
    ) {
      submittedPatch.relay_uri = relayValidation.normalized;
    }
    try {
      setSubmitError("");
      await update.mutateAsync({ id: agentId, ...submittedPatch });
      toast.success(tc("success"));
      onClose();
    } catch (error) {
      const message = formatErrorToast(error, tc("error"));
      setSubmitError(message);
      toast.error(message);
    }
  };

  return (
    <>
      {refreshError ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            <span>{t("editLoadFailed")}</span>
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>{t("retry")}</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <FieldGroup className="gap-5 py-2">
        <Field>
          <FieldLabel htmlFor="edit-agent-name">{tc("name")}</FieldLabel>
          <Input id="edit-agent-name" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
        </Field>
        <StatusSelect value={form.status} onChange={(status) => setForm({ ...form, status })} />
        <Field>
          <FieldLabel htmlFor="edit-agent-tags">{t("tags")}</FieldLabel>
          <Input id="edit-agent-tags" value={form.tags} onChange={(event) => setForm({ ...form, tags: event.target.value })} />
        </Field>
        <AgentAddressEditor value={form.http_addresses} onChange={(http_addresses) => setForm({ ...form, http_addresses })} />
        <Field>
          <FieldLabel htmlFor="edit-agent-proxy">{t("proxyUrl")}</FieldLabel>
          <Input id="edit-agent-proxy" value={form.proxy_url} onChange={(event) => setForm({ ...form, proxy_url: event.target.value })} />
        </Field>
        <AgentRelayConfigFields
          mode={form.relay_mode}
          uri={form.relay_uri}
          effectiveRelayURI={initialDetail.connection.relay.desired.effective_uri}
          activeStreams={initialDetail.connection.relay.active.streams}
          disabled={update.isPending}
          onModeChange={(relay_mode) => setForm({ ...form, relay_mode })}
          onURIChange={(relay_uri) => setForm({ ...form, relay_uri })}
        />
        <FieldSet>
          <FieldLegend variant="label">{t("peerRouteMode")}</FieldLegend>
          <FieldDescription>{t("peerRouteModeDescription")}</FieldDescription>
          <ToggleGroup
            type="single"
            variant="outline"
            value={form.peer_route_mode}
            disabled={update.isPending}
            onValueChange={(value) => {
              if (value === "direct_first" || value === "relay_only") {
                setForm({ ...form, peer_route_mode: value });
              }
            }}
          >
            <ToggleGroupItem value="direct_first">{t("peerRouteDirectFirst")}</ToggleGroupItem>
            <ToggleGroupItem value="relay_only">{t("peerRouteRelayOnly")}</ToggleGroupItem>
          </ToggleGroup>
        </FieldSet>
      </FieldGroup>
      {submitError ? (
        <Alert variant="destructive" role="alert"><AlertDescription>{submitError}</AlertDescription></Alert>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>{tc("cancel")}</Button>
        <Button
          type="button"
          disabled={!hasChanges || update.isPending || Boolean(relayValidation && "error" in relayValidation)}
          onClick={() => void save()}
        >
          {update.isPending ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : null}
          {tc("save")}
        </Button>
      </DialogFooter>
    </>
  );
}
