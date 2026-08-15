"use client";

import { useRef, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { FormErrorSummary, useFormErrorReport } from "@/components/business/form-error-summary";
import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useAPIRole, useCreateAPIRole, useUpdateAPIRole, type APIRole, type APIRoleMember, type APIPrincipalType } from "@/lib/api/api-access";
import { useCapabilities } from "@/lib/api/capabilities";
import { useAuth } from "@/lib/auth";

import { PermissionEditor, type PermissionDraft } from "./permission-editor";
import { isProtectedAPIRole } from "./role-protection";

export type APIRoleFormMode = { kind: "create" } | { kind: "edit"; id: number };

interface RoleDraft { key: string; name: string; description: string; enabled: boolean; permissions: PermissionDraft[]; members: APIRoleMember[]; }
const emptyDraft: RoleDraft = { key: "", name: "", description: "", enabled: true, permissions: [], members: [] };

function draftFrom(role: APIRole): RoleDraft {
  return { key: role.key, name: role.name, description: role.description, enabled: role.status === 1, permissions: role.permissions.map((permission, index) => ({ ...permission, action: "invoke", rowKey: index + 1, scope: permission.resource === "api_route" ? "specific" : permission.resource_id === 0 ? "all" : "specific" })), members: role.members };
}

const memberEntities = { user: "user", user_group: "user-group", token: "api-access-token" } as const;
const memberTypes: APIPrincipalType[] = ["user", "user_group", "token"];
function memberIDs(members: APIRoleMember[], type: APIPrincipalType) { return members.filter((member) => member.principal_type === type).map((member) => String(member.principal_id)); }
function replaceMembers(members: APIRoleMember[], type: APIPrincipalType, ids: string[]) {
  const replacements = ids.map(Number).filter((id) => Number.isSafeInteger(id) && id > 0).map((principal_id) => ({ principal_type: type, principal_id }));
  return memberTypes.flatMap((currentType) => currentType === type ? replacements : members.filter((member) => member.principal_type === currentType));
}

function statusOf(error: unknown) { return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined; }
const isPositiveSafeInteger = (value: number | undefined): value is number => typeof value === "number" && Number.isSafeInteger(value) && value > 0;

export function RoleFormPageSkeleton({ mode }: { mode: APIRoleFormMode["kind"] }) {
  const t = useTranslations("apiAccess");
  return <PageLayout title={t(mode === "edit" ? "editRole" : "createRole")} description={t("roleFormDescription")} maxWidth="3xl"><Skeleton className="h-80 w-full" /></PageLayout>;
}

export function permissionValidationError(rows: PermissionDraft[], resolvedServiceIDs = new Map<number, number>()) {
  for (const row of rows) {
    if (row.resource === "api_route" && row.scope !== "specific") return "permissionTargetRequired";
    if (row.scope !== "specific") continue;
    if (!isPositiveSafeInteger(row.resource_id)) return "permissionTargetRequired";
    if (row.resource === "api_route" && !isPositiveSafeInteger(row.apiServiceId ?? resolvedServiceIDs.get(row.rowKey))) return "permissionServiceRequired";
  }
  return undefined;
}

export function RoleForm({ mode }: { mode: APIRoleFormMode }) {
  const t = useTranslations("apiAccess");
  const tc = useTranslations("common");
  const router = useRouter();
  const { user } = useAuth();
  const capability = useCapabilities(user?.user_id);
  const accessAllowed = capability.data?.generic_api?.access === true && !capability.error && !capability.isLoading && !capability.isPending;
  const query = useAPIRole(mode.kind === "edit" ? mode.id : 0, { enabled: mode.kind === "edit" && accessAllowed });
  const create = useCreateAPIRole();
  const update = useUpdateAPIRole();
  const identity = mode.kind === "edit" ? `edit:${mode.id}` : "create";
  const queryMatches = mode.kind === "edit" && query.data?.id === mode.id;
  const [draft, setDraft] = useState<{ identity: string; value: RoleDraft }>();
  const resolvedServiceIDs = useRef(new Map<number, number>());
  const values = draft?.identity === identity ? draft.value : queryMatches && query.data ? draftFrom(query.data) : emptyDraft;
  const setValues = (change: (current: RoleDraft) => RoleDraft) => setDraft({ identity, value: change(values) });
  const { error, clearError, reportError } = useFormErrorReport();
  const pending = create.isPending || update.isPending;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    clearError();
    const permissionError = permissionValidationError(values.permissions, resolvedServiceIDs.current);
    if (permissionError) { reportError(t(permissionError)); return; }
    const body = { key: values.key.trim(), name: values.name.trim(), description: values.description, status: values.enabled ? 1 : 0, permissions: values.permissions.map(({ resource, resource_id, action }) => ({ resource, resource_id, action })), members: values.members };
    try {
      if (mode.kind === "edit") await update.mutateAsync({ id: mode.id, ...body });
      else await create.mutateAsync(body);
      toast.success(tc("success"));
      router.push("/api-access?tab=roles");
    } catch (reason) { reportError(reason instanceof Error ? reason.message : t("mutationFailed")); }
  };

  if (capability.isPending || capability.isLoading) return <RoleFormPageSkeleton mode={mode.kind} />;
  if (capability.error || capability.data?.generic_api?.access !== true) return <PageLayout title={t(mode.kind === "edit" ? "editRole" : "createRole")}><Alert variant={capability.error ? "destructive" : "default"}><AlertTitle>{capability.error ? t(statusOf(capability.error) === 403 ? "permissionDenied" : "loadFailed") : t("unavailable")}</AlertTitle><AlertDescription>{capability.error ? t("loadFailedDescription") : t("permissionRequired")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind === "edit" && query.isLoading) return <RoleFormPageSkeleton mode="edit" />;
  if (mode.kind === "edit" && query.error) return <PageLayout title={t("editRole")}><Alert variant="destructive"><AlertTitle>{t(statusOf(query.error) === 403 ? "permissionDenied" : "loadFailed")}</AlertTitle><AlertDescription>{t("loadFailedDescription")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind === "edit" && !queryMatches) return <PageLayout title={t("editRole")}><Alert><AlertTitle>{t("roleNotFound")}</AlertTitle><AlertDescription>{t("roleNotFoundDescription")}</AlertDescription></Alert></PageLayout>;
  if (mode.kind === "edit" && query.data && isProtectedAPIRole(query.data)) return <PageLayout title={t("editRole")}><Alert><AlertTitle>{t("builtInProtected")}</AlertTitle><AlertDescription>{t("builtInProtectedDescription")}</AlertDescription></Alert></PageLayout>;

  return <PageLayout title={t(mode.kind === "edit" ? "editRole" : "createRole")} description={t("roleFormDescription")} maxWidth="3xl" footer={<><Button type="button" variant="outline" onClick={() => router.push("/api-access?tab=roles")}>{t("cancel")}</Button><Button type="submit" form="api-role-form" disabled={pending}>{t("save")}</Button></>}>
    <form id="api-role-form" onSubmit={submit} className="flex flex-col gap-6">
      <FormErrorSummary error={error} title={t("mutationFailed")} />
      <FieldGroup>
        <Field><FieldLabel htmlFor="api-role-key">{t("key")}</FieldLabel><Input id="api-role-key" value={values.key} onChange={(event) => setValues((current) => ({ ...current, key: event.target.value }))} required /></Field>
        <Field><FieldLabel htmlFor="api-role-name">{t("name")}</FieldLabel><Input id="api-role-name" value={values.name} onChange={(event) => setValues((current) => ({ ...current, name: event.target.value }))} required /></Field>
        <Field><FieldLabel htmlFor="api-role-description">{t("descriptionField")}</FieldLabel><Textarea id="api-role-description" value={values.description} onChange={(event) => setValues((current) => ({ ...current, description: event.target.value }))} /></Field>
        <Field orientation="horizontal"><Switch id="api-role-enabled" checked={values.enabled} onCheckedChange={(enabled) => setValues((current) => ({ ...current, enabled }))} /><FieldLabel htmlFor="api-role-enabled">{t("enabled")}</FieldLabel></Field>
      </FieldGroup>
      <PermissionEditor rows={values.permissions} resolvedServiceIDs={resolvedServiceIDs} onChange={(permissions) => setValues((current) => ({ ...current, permissions }))} onAdd={() => { const rowKey = Math.max(0, ...values.permissions.map((permission) => permission.rowKey)) + 1; setValues((current) => ({ ...current, permissions: [...current.permissions, { rowKey, resource: "api_service", resource_id: 0, action: "invoke", scope: "all" }] })); }} />
      <FieldSet>
        <FieldLegend variant="label">{t("members")}</FieldLegend>
        <FieldDescription>{t("membersDescription")}</FieldDescription>
        <FieldGroup>
          {memberTypes.map((type) => <Field key={type}><FieldLabel>{t(`principalTypeOptions.${type}`)}</FieldLabel><EntityMultiPicker entity={memberEntities[type]} value={memberIDs(values.members, type)} onChange={(ids) => setValues((current) => ({ ...current, members: replaceMembers(current.members, type, ids) }))} /></Field>)}
        </FieldGroup>
      </FieldSet>
    </form>
  </PageLayout>;
}
