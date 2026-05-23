"use client";

import { useEffect, useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import {
  useCreateOAuthProvider,
  useUpdateOAuthProvider,
} from "@/lib/api/oauth";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { OAuthProvider } from "@/lib/types-oauth";

type Mode = "create" | "edit";
type FormState = Partial<OAuthProvider>;

interface Props {
  mode: Mode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: OAuthProvider;
}

// 编辑时允许提交的字段（与后端 allowedUpdateKeys 一一对应）
const UPDATE_KEYS = [
  "display_name", "issuer", "authorization_endpoint", "token_endpoint",
  "userinfo_endpoint", "jwks_uri", "client_id", "client_secret",
  "scopes", "icon_url", "enabled",
] as const;

// 创建时允许提交的字段（含 name + protocol，因为 create 需要 slug 且 protocol 不可 update）
const CREATE_KEYS = ["name", "protocol", ...UPDATE_KEYS] as const;

function pickKeys<T extends object, K extends keyof T>(
  obj: Partial<T>,
  keys: readonly K[],
): Partial<Pick<T, K>> {
  const out: Partial<Pick<T, K>> = {};
  for (const k of keys) {
    if (obj[k] !== undefined) out[k] = obj[k];
  }
  return out;
}

function pickFormState(p: OAuthProvider): FormState {
  return {
    name: p.name,
    protocol: p.protocol ?? "oidc",
    display_name: p.display_name,
    issuer: p.issuer,
    authorization_endpoint: p.authorization_endpoint,
    token_endpoint: p.token_endpoint,
    userinfo_endpoint: p.userinfo_endpoint,
    jwks_uri: p.jwks_uri,
    client_id: p.client_id,
    client_secret: "",
    scopes: p.scopes,
    icon_url: p.icon_url,
    enabled: p.enabled,
  };
}

const TEXT_FIELDS: {
  key: keyof OAuthProvider;
  labelKey: string;
  type?: "url" | "text";
  required?: boolean;
  placeholder?: string;
}[] = [
  { key: "name", labelKey: "name", required: true, placeholder: "github" },
  { key: "display_name", labelKey: "displayName", required: true },
  { key: "issuer", labelKey: "issuer", type: "url" },
  { key: "authorization_endpoint", labelKey: "authorizationEndpoint", type: "url", required: true },
  { key: "token_endpoint", labelKey: "tokenEndpoint", type: "url", required: true },
  { key: "userinfo_endpoint", labelKey: "userinfoEndpoint", type: "url", required: true },
  { key: "jwks_uri", labelKey: "jwksUri", type: "url" },
  { key: "client_id", labelKey: "clientId", required: true },
  { key: "scopes", labelKey: "scopes", placeholder: "openid profile email" },
  { key: "icon_url", labelKey: "iconUrl", type: "url" },
];

export function ProviderFormDialog({ mode, open, onOpenChange, initial }: Props) {
  const t = useTranslations("oauth.providers");
  const tc = useTranslations("common");
  const create = useCreateOAuthProvider();
  const update = useUpdateOAuthProvider();
  const [form, setForm] = useState<FormState>({});

  useEffect(() => {
    if (!open) return;
    if (mode === "create") {
      setForm({ enabled: true, protocol: "oidc" });
    } else if (initial) {
      setForm(pickFormState(initial));
    }
  }, [open, mode, initial]);

  const isSubmitting = create.isPending || update.isPending;

  const handleSubmit = async () => {
    try {
      if (mode === "create") {
        const body = pickKeys(form, CREATE_KEYS);
        await create.mutateAsync(body);
      } else if (initial) {
        const body = pickKeys(form, UPDATE_KEYS);
        if (!body.client_secret) delete body.client_secret;
        await update.mutateAsync({ id: initial.id, ...body });
      }
      toast.success(tc("success"));
      onOpenChange(false);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const tLabel = t as unknown as (k: string) => string;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{mode === "create" ? t("addNew") : tc("edit")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <Label htmlFor="protocol">协议</Label>
            <Select
              value={form.protocol ?? "oidc"}
              disabled={mode === "edit"}
              onValueChange={(v) =>
                setForm((p) => ({ ...p, protocol: v as "oidc" | "feishu" }))
              }
            >
              <SelectTrigger id="protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="oidc">标准 OIDC（GitHub / Google / Keycloak 等）</SelectItem>
                <SelectItem value="feishu">飞书（Lark）</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {TEXT_FIELDS.map((f) => {
            if (form.protocol === "feishu" && (f.key === "issuer" || f.key === "jwks_uri")) {
              return null;
            }
            return (
              <div key={f.key as string} className="space-y-1.5">
                <Label htmlFor={f.key as string}>
                  {tLabel(f.labelKey)}
                  {f.required && <span className="ml-1 text-destructive">*</span>}
                </Label>
                <Input
                  id={f.key as string}
                  type={f.type ?? "text"}
                  placeholder={f.placeholder}
                  disabled={mode === "edit" && f.key === "name"}
                  value={(form[f.key] as string | undefined) ?? ""}
                  onChange={(e) =>
                    setForm((p) => ({ ...p, [f.key]: e.target.value }))
                  }
                />
              </div>
            );
          })}
          <div className="space-y-1.5">
            <Label htmlFor="client_secret">
              Client Secret
              {mode === "create" && <span className="ml-1 text-destructive">*</span>}
            </Label>
            <Input
              id="client_secret"
              type="password"
              placeholder={mode === "edit" ? "***" : ""}
              value={(form.client_secret as string | undefined) ?? ""}
              onChange={(e) =>
                setForm((p) => ({ ...p, client_secret: e.target.value }))
              }
            />
            {mode === "edit" && (
              <p className="text-xs text-muted-foreground">{t("secretChangeHint")}</p>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Switch
              id="enabled"
              checked={form.enabled ?? true}
              onCheckedChange={(v) => setForm((p) => ({ ...p, enabled: v }))}
            />
            <Label htmlFor="enabled">{tc("enabled")}</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {tc("cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={isSubmitting}>
            {isSubmitting && <Loader2 className="mr-2 size-4 animate-spin" />}
            {tc("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
