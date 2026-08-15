"use client";

import { useState, type FormEvent } from "react";
import { useTranslations } from "next-intl";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import type { EntityName } from "@/components/business/entity-picker/registry";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAPIRole, useCreateAPIRoleBinding, useUpdateAPIRoleBinding, type APIPrincipalType, type APIRoleBinding } from "@/lib/api/api-access";

const principalTypes: APIPrincipalType[] = ["user", "user_group", "token"];
const principalEntities: Record<APIPrincipalType, EntityName> = { user: "user", user_group: "user-group", token: "token" };

interface BindingDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  binding: APIRoleBinding | null;
}

function initialValues(binding: APIRoleBinding | null) {
  return { principalType: binding?.principal_type ?? "user" as APIPrincipalType, principalID: binding ? String(binding.principal_id) : "", roleID: binding ? String(binding.role_id) : "" };
}

export function BindingDialog({ open, onOpenChange, binding }: BindingDialogProps) {
  const t = useTranslations("apiAccess");
  const create = useCreateAPIRoleBinding();
  const update = useUpdateAPIRoleBinding();
  const identity = binding ? `edit:${binding.id}` : "create";
  const [draft, setDraft] = useState(() => ({ identity, ...initialValues(binding) }));
  const values = draft.identity === identity ? draft : { identity, ...initialValues(binding) };
  const setValues = (next: Partial<typeof values>) => setDraft({ ...values, ...next, identity });
  const [error, setError] = useState<string>();
  const selectedRoleID = Number(values.roleID);
  const selectedRole = useAPIRole(selectedRoleID, { enabled: Number.isSafeInteger(selectedRoleID) && selectedRoleID > 0 });
  const principalEntity = principalEntities[values.principalType];
  const pending = create.isPending || update.isPending;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    const principalID = Number(values.principalID);
    if (!Number.isSafeInteger(principalID) || principalID <= 0 || selectedRole.isLoading || !selectedRole.data || selectedRole.data.id !== selectedRoleID || selectedRole.data.status !== 1 || selectedRole.data.built_in) {
      setError(t("invalidBinding"));
      return;
    }
    const body = { principal_type: values.principalType, principal_id: principalID, role_id: selectedRoleID };
    try {
      if (binding) await update.mutateAsync({ id: binding.id, ...body });
      else await create.mutateAsync(body);
      onOpenChange(false);
    } catch (reason) { setError(reason instanceof Error ? reason.message : t("mutationFailed")); }
  };

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent>
      <DialogHeader><DialogTitle>{binding ? t("editBinding") : t("createBinding")}</DialogTitle><DialogDescription>{t("bindingDialogDescription")}</DialogDescription></DialogHeader>
      <form onSubmit={submit} className="flex flex-col gap-5">
        {error ? <Alert variant="destructive"><AlertTitle>{t("mutationFailed")}</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}
        <FieldGroup className="gap-5">
          <Field><FieldLabel htmlFor="api-binding-principal-type">{t("principalType")}</FieldLabel><Select value={values.principalType} onValueChange={(principalType) => setValues({ principalType: principalType as APIPrincipalType, principalID: "" })}><SelectTrigger id="api-binding-principal-type" aria-label={t("principalType")}><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{principalTypes.map((type) => <SelectItem key={type} value={type}>{t(`principalTypeOptions.${type}`)}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>
          <Field><FieldLabel htmlFor="api-binding-principal">{t("principal")}</FieldLabel><EntityPicker key={principalEntity} id="api-binding-principal" entity={principalEntity} value={values.principalID} onChange={(principalID) => setValues({ principalID })} /></Field>
          <Field><FieldLabel htmlFor="api-binding-role">{t("role")}</FieldLabel><EntityPicker id="api-binding-role" entity="api-role" value={values.roleID} onChange={(roleID) => setValues({ roleID })} /></Field>
        </FieldGroup>
        <DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("cancel")}</Button><Button type="submit" disabled={pending || !values.principalID || !values.roleID}>{t("save")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>;
}
