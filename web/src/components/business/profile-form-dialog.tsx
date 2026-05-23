"use client";

import { useEffect, useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import {
  ProfileFormFields,
  type ProfileFormValue,
} from "@/components/business/profile-form-fields";
import { PasswordInput } from "@/components/business/password-input";
import { StatusSelect } from "@/components/business/status-select";
import { GroupSelect } from "@/components/business/group-select";

import { useUpdateProfile, useUpdateUser } from "@/lib/api/users";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { User } from "@/lib/types";

interface SelfProps {
  mode: "self";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial: ProfileFormValue;
  fallbackInitial?: string;
}

interface AdminProps {
  mode: "admin";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  user: User;
}

export type ProfileFormDialogProps = SelfProps | AdminProps;

const DEFAULT_GROUP_ID = 1;

function diffProfile(
  current: ProfileFormValue,
  initial: ProfileFormValue,
): Partial<ProfileFormValue> {
  const out: Partial<ProfileFormValue> = {};
  if (current.email !== initial.email) out.email = current.email;
  if (current.display_name !== initial.display_name) out.display_name = current.display_name;
  if (current.avatar_url !== initial.avatar_url) out.avatar_url = current.avatar_url;
  return out;
}

function hasHTTPScheme(s: string): boolean {
  return s.startsWith("http://") || s.startsWith("https://");
}

export function ProfileFormDialog(props: ProfileFormDialogProps) {
  return props.mode === "self" ? <SelfDialog {...props} /> : <AdminDialog {...props} />;
}

function SelfDialog(props: SelfProps) {
  const t = useTranslations("profile");
  const tc = useTranslations("common");

  const [value, setValue] = useState<ProfileFormValue>(props.initial);
  const [emailError, setEmailError] = useState<string>("");
  const [avatarError, setAvatarError] = useState<string>("");

  const mutation = useUpdateProfile();

  useEffect(() => {
    if (props.open) {
      setValue(props.initial);
      setEmailError("");
      setAvatarError("");
    }
  }, [props.open, props.initial]);

  const diff = diffProfile(value, props.initial);
  const isDirty = Object.keys(diff).length > 0;

  const handleChange = (next: ProfileFormValue) => {
    setValue(next);
    if (next.email !== value.email) setEmailError("");
    if (next.avatar_url !== value.avatar_url) setAvatarError("");
  };

  const handleSubmit = async () => {
    if (value.avatar_url.trim() && !hasHTTPScheme(value.avatar_url.trim())) {
      setAvatarError(t("avatarInvalidUrl"));
      return;
    }
    try {
      await mutation.mutateAsync(diff);
      toast.success(t("profileSaved"));
      props.onOpenChange(false);
    } catch (e: unknown) {
      const msg = (e as { body?: { error?: string } })?.body?.error;
      if (msg === "email_taken") setEmailError(t("emailTaken"));
      else toast.error(t("profileSaveFailed"));
    }
  };

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("editProfile")}</DialogTitle>
        </DialogHeader>
        <ProfileFormFields
          value={value}
          onChange={handleChange}
          fallbackInitial={props.fallbackInitial}
          emailError={emailError}
          avatarError={avatarError}
          disabled={mutation.isPending}
          idPrefix="pf-self"
        />
        <DialogFooter>
          <Button variant="outline" onClick={() => props.onOpenChange(false)} disabled={mutation.isPending}>
            {tc("cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={mutation.isPending || !isDirty}>
            {tc("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface AdminLocalState {
  fields: ProfileFormValue;
  username: string;
  password: string;
  role: string;
  status: string;
  group_id: number;
}

function buildAdminInitial(user: User): AdminLocalState {
  return {
    fields: {
      email: user.email ?? "",
      display_name: user.display_name ?? "",
      avatar_url: user.avatar_url ?? "",
    },
    username: user.username,
    password: "",
    role: String(user.role),
    status: String(user.status),
    group_id: user.group_id ?? DEFAULT_GROUP_ID,
  };
}

function AdminDialog(props: AdminProps) {
  const t = useTranslations("profile");
  const tu = useTranslations("users");
  const tc = useTranslations("common");

  const [state, setState] = useState<AdminLocalState>(() => buildAdminInitial(props.user));
  const [avatarError, setAvatarError] = useState<string>("");

  const mutation = useUpdateUser();

  useEffect(() => {
    if (props.open) {
      setState(buildAdminInitial(props.user));
      setAvatarError("");
    }
  }, [props.open, props.user]);

  const updateFields = (next: ProfileFormValue) => {
    setState({ ...state, fields: next });
    if (next.avatar_url !== state.fields.avatar_url) setAvatarError("");
  };

  const handleSubmit = async () => {
    if (state.fields.avatar_url.trim() && !hasHTTPScheme(state.fields.avatar_url.trim())) {
      setAvatarError(t("avatarInvalidUrl"));
      return;
    }
    try {
      await mutation.mutateAsync({
        id: props.user.id,
        username: state.username,
        ...(state.password ? { password: state.password } : {}),
        email: state.fields.email,
        display_name: state.fields.display_name,
        avatar_url: state.fields.avatar_url,
        role: Number(state.role),
        status: Number(state.status),
        group_id: state.group_id,
      });
      toast.success(tc("success"));
      props.onOpenChange(false);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {tu("editUser")} · {props.user.username}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <ProfileFormFields
            value={state.fields}
            onChange={updateFields}
            fallbackInitial={props.user.username}
            avatarError={avatarError}
            disabled={mutation.isPending}
            idPrefix="pf-admin"
          />

          <Separator />
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {tu("adminFields")}
          </p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="pf-admin-username">{tu("username")}</Label>
              <Input
                id="pf-admin-username"
                value={state.username}
                onChange={(e) => setState({ ...state, username: e.target.value })}
                disabled={mutation.isPending}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-admin-role">{tu("role")}</Label>
              <Select
                value={state.role}
                onValueChange={(v) => setState({ ...state, role: v })}
                disabled={mutation.isPending}
              >
                <SelectTrigger id="pf-admin-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="1">{tu("roleUser")}</SelectItem>
                  <SelectItem value="2">{tu("roleAdmin")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>{tu("group")}</Label>
              <GroupSelect
                value={state.group_id}
                onChange={(id) => setState({ ...state, group_id: id })}
              />
            </div>
            <div className="space-y-1.5">
              <Label>{tc("status")}</Label>
              <StatusSelect
                value={state.status}
                onChange={(v) => setState({ ...state, status: v })}
                showLabel={false}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="pf-admin-password">{tu("resetPassword")}</Label>
            <PasswordInput
              value={state.password}
              onChange={(v) => setState({ ...state, password: v })}
              placeholder={tu("passwordEmptyMeansUnchanged")}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => props.onOpenChange(false)} disabled={mutation.isPending}>
            {tc("cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={mutation.isPending}>
            {tc("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
