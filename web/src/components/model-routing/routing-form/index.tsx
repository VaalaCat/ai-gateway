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
import { ROUTING_ERROR_KEYS } from "@/lib/api/model-routings";
import { ApiError } from "@/lib/api/client";
import { useTokens } from "@/lib/api/tokens";
import { useUserPref } from "@/hooks/use-user-pref";
import { FormMode, RoutingFormValues } from "./types";

export interface RoutingFormProps {
  mode: FormMode;
  apiMode?: "admin" | "user";
}

export function RoutingForm({ mode, apiMode = "admin" }: RoutingFormProps) {
  const t = useTranslations("modelRoutings");
  const tc = useTranslations("common");
  const router = useRouter();
  const { form, isLoading, createMut, updateMut } = useRoutingForm(mode, apiMode);

  // user 模式：拉 tokens、用 useUserPref 维护当前选中
  // useTokens 现签名 useTokens(params?)，不接 enabled。无条件调；
  // admin 模式下结果不消费，多一次 GET /tokens 可接受。
  const tokensQuery = useTokens();
  const userTokens = apiMode === "user" ? tokensQuery.data?.data ?? [] : [];

  const [storedTokenID, setStoredTokenID] = useUserPref<string>(
    "routing-form-token-id",
    "",
  );

  const selectedTokenID = useMemo(() => {
    if (apiMode !== "user") return "";
    if (storedTokenID && userTokens.some((tok) => String(tok.id) === storedTokenID)) {
      return storedTokenID;
    }
    return userTokens.length > 0 ? String(userTokens[0].id) : "";
  }, [apiMode, storedTokenID, userTokens]);

  const selectedToken = userTokens.find((tok) => String(tok.id) === selectedTokenID);
  const tokenKey = selectedToken?.key ?? null;

  const tokenOptions = userTokens.map((tok) => ({
    id: String(tok.id),
    name: tok.name,
  }));

  const handleSelectTokenID = (id: string) => setStoredTokenID(id);

  const onSubmit = async (values: RoutingFormValues) => {
    try {
      if (mode.kind === "new") {
        await createMut.mutateAsync(values);
      } else {
        await updateMut.mutateAsync({ id: mode.id, ...values });
      }
      toast.success(tc("success"));
      const backHref = apiMode === "admin" ? "/model-routings" : "/profile/model-routings";
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

  if (isLoading) {
    return <div className="text-muted-foreground">Loading...</div>;
  }

  // user 模式且确认无 token：显示 CTA，跳过表单本体
  if (apiMode === "user" && !tokensQuery.isLoading && userTokens.length === 0) {
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
            tokenOptions={tokenOptions}
          />
          <MembersSection form={form} apiMode={apiMode} tokenKey={tokenKey} />
        </div>
        <div className="min-w-0">
          <PreviewPanel form={form} apiMode={apiMode} />
        </div>
        <SaveBar form={form} />
      </form>
    </Form>
  );
}
