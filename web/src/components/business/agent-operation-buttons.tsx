"use client";

import { useState } from "react";
import { LoaderCircle, MoreHorizontal, Pause, Radar, RefreshCw, Unplug } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

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
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useAgentOperation, useEnqueueConnectivityProbe } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { AgentOperation, ConnectionSnapshot } from "@/lib/types";

type ConfirmOperation = "relay_drain" | "relay_disconnect";

export function AgentOperationButtons({ agentId, snapshot, stale = false, loading = false }: { agentId: number; snapshot?: ConnectionSnapshot; stale?: boolean; loading?: boolean }) {
  const t = useTranslations("agents.connection");
  const probe = useEnqueueConnectivityProbe();
  const operation = useAgentOperation();
  const [confirming, setConfirming] = useState<ConfirmOperation | null>(null);

  const request = snapshot ? {
    expected_epoch: snapshot.snapshot_epoch,
    expected_control_generation: snapshot.control.session_generation,
    expected_relay_generation: snapshot.relay.active.session_generation,
  } : undefined;
  const statuses = new Map(snapshot?.allowed_operations.map((item) => [item.operation, item]));
  const groupPending = probe.isPending || operation.isPending;

  const disabledReason = (name: AgentOperation) => {
    if (loading || !snapshot) return t("loadingOperations");
    if (stale) return t("staleDenied");
    const status = statuses.get(name);
    return status?.allowed ? "" : status?.denial_code || t("operationDenied");
  };

  const run = async (name: AgentOperation) => {
    if (!request || disabledReason(name) || groupPending) return;
    try {
      if (name === "probe") {
        await probe.mutateAsync({ id: agentId, request });
        return;
      }
      await operation.mutateAsync({ id: agentId, operation: name, request });
    } catch (error) {
      toast.error(formatErrorToast(error, t("operationFailed")));
    }
  };

  const relayActions = [
    { name: "relay_reconnect" as const, label: t("reconnect"), icon: RefreshCw, confirm: false },
    { name: "relay_drain" as const, label: t("drain"), icon: Pause, confirm: true },
    { name: "relay_disconnect" as const, label: t("disconnect"), icon: Unplug, confirm: true },
  ];

  return (
    <>
      <div className="flex shrink-0 items-center gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex" tabIndex={disabledReason("probe") ? 0 : -1}>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="size-11 sm:size-8"
                aria-label={t("probe")}
                disabled={Boolean(disabledReason("probe")) || groupPending}
                onClick={() => void run("probe")}
              >
                {probe.isPending ? <LoaderCircle className="animate-spin" /> : <Radar />}
              </Button>
            </span>
          </TooltipTrigger>
          <TooltipContent>{disabledReason("probe") || t("probe")}</TooltipContent>
        </Tooltip>
        <div className="hidden items-center gap-1 sm:flex">
          {relayActions.map(({ name, label, icon: Icon, confirm }) => {
          const reason = disabledReason(name);
          const pending = operation.isPending && operation.variables?.operation === name;
          return (
            <Tooltip key={name}>
              <TooltipTrigger asChild>
                <span className="inline-flex" tabIndex={reason ? 0 : -1}>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={label}
                    disabled={!!reason || groupPending}
                    onClick={() => confirm ? setConfirming(name as ConfirmOperation) : void run(name)}
                  >
                    {pending ? <LoaderCircle className="animate-spin" /> : <Icon />}
                  </Button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{reason || label}</TooltipContent>
            </Tooltip>
          );
        })}
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="size-11 sm:hidden"
              aria-label={t("moreRelayActions")}
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuGroup>
              {relayActions.map(({ name, label, icon: Icon, confirm }) => (
                <DropdownMenuItem
                  key={name}
                  disabled={Boolean(disabledReason(name)) || groupPending}
                  onSelect={() => confirm ? setConfirming(name as ConfirmOperation) : void run(name)}
                >
                  <Icon />
                  {label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <AlertDialog open={confirming !== null} onOpenChange={(open) => { if (!open) setConfirming(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(confirming === "relay_disconnect" ? "confirmDisconnect" : "confirmDrain")}</AlertDialogTitle>
            <AlertDialogDescription>{t("confirmDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={!confirming || !!(confirming && disabledReason(confirming)) || groupPending}
              onClick={() => {
                if (!confirming || disabledReason(confirming) || groupPending) return;
                void run(confirming);
                setConfirming(null);
              }}
            >
              {t("confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
