"use client";

import { useMemo } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Form } from "@/components/ui/form";
import { Button } from "@/components/ui/button";
import { useRoutingForm } from "./use-routing-form";
import { BasicSection } from "./sections/basic";
import { MembersSection } from "./sections/members";
import { SaveBar } from "./save-bar";
import { PreviewPanel } from "../preview-panel";
import {
  type ModelRoutingOwner,
  ROUTING_ERROR_KEYS,
  useRoutingCandidatesByToken,
} from "@/lib/api/model-routings";
import { ApiError } from "@/lib/api/client";
import { useToken, useTokens } from "@/lib/api/tokens";
import { useUserPref } from "@/hooks/use-user-pref";
import { FormMode, RoutingFormValues } from "./types";

export interface RoutingFormProps {
  mode: FormMode;
  apiMode?: "admin" | "user";
  tokenId?: number;
}

export function RoutingForm({ mode, apiMode = "admin", tokenId }: RoutingFormProps) {
  const t = useTranslations("modelRoutings");
  const tc = useTranslations("common");
  const router = useRouter();
  const owner = useMemo<ModelRoutingOwner>(
    () => tokenId === undefined ? { kind: "scope" } : { kind: "token", tokenId },
    [tokenId],
  );
  const {
    form,
    isLoading: isRoutingLoading,
    createMut,
    updateMut,
  } = useRoutingForm(mode, apiMode, owner);
  const tokenQuery = useToken(tokenId ?? 0);

  // user scope 模式保留可切换的 Token 候选；Token owner 模式由 URL 固定。
  const tokensQuery = useTokens(
    {},
    { enabled: apiMode === "user" && tokenId === undefined },
  );
  const userTokens = useMemo(
    () => apiMode === "user" && tokenId === undefined ? tokensQuery.data?.data ?? [] : [],
    [apiMode, tokenId, tokensQuery.data?.data],
  );

  const [storedTokenID, setStoredTokenID] = useUserPref<string>(
    "routing-form-token-id",
    "",
  );

  const selectedTokenID = useMemo(() => {
    if (tokenId !== undefined) return String(tokenId);
    if (apiMode !== "user") return "";
    if (storedTokenID && userTokens.some((tok) => String(tok.id) === storedTokenID)) {
      return storedTokenID;
    }
    return userTokens.length > 0 ? String(userTokens[0].id) : "";
  }, [apiMode, storedTokenID, tokenId, userTokens]);

  const selectedToken = tokenId === undefined
    ? userTokens.find((tok) => String(tok.id) === selectedTokenID)
    : tokenQuery.data;
  const tokenKey = selectedToken?.key ?? null;
  const tokenCandidates = useRoutingCandidatesByToken(
    tokenId === undefined ? null : tokenKey,
  );

  const handleSelectTokenID = (id: string) => setStoredTokenID(id);
  const backHref = tokenId === undefined
    ? apiMode === "admin" ? "/model-routings" : "/profile/model-routings"
    : `/tokens?selected=${tokenId}`;

  const onSubmit = async (values: RoutingFormValues) => {
    try {
      if (mode.kind === "new") {
        await createMut.mutateAsync(values);
      } else {
        await updateMut.mutateAsync({ id: mode.id, ...values });
      }
      toast.success(tc("success"));
      router.push(backHref);
    } catch (e) {
      handleSubmitError(e);
    }
  };

  function handleSubmitError(e: unknown) {
    const err = e as ApiError;
    const body = (err.body ?? {}) as { code?: string; details?: Record<string, unknown> };
    const code = body.code;

    if (code === "duplicate_name" || code === "name_contains_comma") {
      form.setError("name", { message: t(ROUTING_ERROR_KEYS[code] as never, body.details as never) });
      return;
    }
    if (code === "invalid_ref") {
      const ref = body.details?.ref as string | undefined;
      const idx = form.getValues("members").findIndex((m) => m.ref === ref);
      if (idx >= 0) {
        form.setError(`members.${idx}.ref` as const, {
          message: t(ROUTING_ERROR_KEYS[code] as never, body.details as never),
        });
        return;
      }
    }
    if (code && ROUTING_ERROR_KEYS[code]) {
      toast.error(t(ROUTING_ERROR_KEYS[code] as never, body.details as never));
      return;
    }
    toast.error(err.message);
  }

  if (isRoutingLoading || (tokenId !== undefined && tokenQuery.isLoading)) {
    return <div className="text-muted-foreground">Loading...</div>;
  }

  // user 模式且确认无 token：显示 CTA，跳过表单本体
  if (
    tokenId === undefined &&
    apiMode === "user" &&
    !tokensQuery.isLoading &&
    userTokens.length === 0
  ) {
    return (
      <div className="flex flex-col items-start gap-3 rounded-lg border border-dashed p-6">
        <p className="text-sm text-muted-foreground">{t("field.noTokensTitle")}</p>
        <Button asChild>
          <Link href="/profile/tokens">{t("field.goToTokens")}</Link>
        </Button>
      </div>
    );
  }

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className="grid gap-6 min-w-0 lg:grid-cols-[1fr_400px]"
      >
        {/* min-w-0 关键：grid item 默认 min-width:auto，长内容会撑开列、
            导致 mobile 横向溢出；显式归零让 track 可收缩 */}
        <div className="space-y-6 min-w-0">
          <BasicSection
            form={form}
            apiMode={apiMode}
            selectedTokenID={selectedTokenID}
            onSelectTokenID={handleSelectTokenID}
            lockedTokenID={tokenId}
          />
          <MembersSection
            form={form}
            apiMode={apiMode}
            tokenKey={tokenKey}
            limitCandidatesToToken={tokenId !== undefined}
          />
        </div>
        <div className="min-w-0">
          <PreviewPanel
            form={form}
            apiMode={apiMode}
            owner={owner}
            allowedRefs={tokenCandidates.data?.visible_refs}
            checkAlias={mode.kind === "edit"}
          />
        </div>
        <SaveBar form={form} onCancel={() => router.push(backHref)} />
      </form>
    </Form>
  );
}
