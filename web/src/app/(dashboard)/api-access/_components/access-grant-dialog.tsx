"use client";

import { useState, type FormEvent } from "react";
import { useTranslations } from "next-intl";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import type { EntityName } from "@/components/business/entity-picker/registry";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useReplaceAPIAccessGrant, type APIAccessGrant, type APIAccessScope, type APIPrincipalType } from "@/lib/api/api-access";

const principalTypes: APIPrincipalType[] = ["user", "user_group", "token"];
const principalEntities: Record<APIPrincipalType, EntityName> = { user: "user", user_group: "user-group", token: "api-access-token" };

export function AccessGrantDialog({ open, onOpenChange, grant }: { open: boolean; onOpenChange: (open: boolean) => void; grant?: APIAccessGrant }) {
  const t = useTranslations("apiAccess");
  const replace = useReplaceAPIAccessGrant();
  const [principalType, setPrincipalType] = useState<APIPrincipalType>(grant?.principal_type ?? "user");
  const [principalID, setPrincipalID] = useState(grant ? String(grant.principal_id) : "");
  const [serviceID, setServiceID] = useState(grant ? String(grant.api_service_id) : "");
  const [scope, setScope] = useState<APIAccessScope>(grant?.configured?.scope ?? "service");
  const [routeIDs, setRouteIDs] = useState<string[]>(grant?.configured?.route_ids.map(String) ?? []);
  const [error, setError] = useState<string>();
  const selectedServiceID = Number(serviceID);
  const isEditing = grant !== undefined;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const selectedPrincipalID = Number(principalID);
    const selectedRouteIDs = routeIDs.map(Number).filter((id) => Number.isSafeInteger(id) && id > 0);
    if (!Number.isSafeInteger(selectedPrincipalID) || selectedPrincipalID <= 0 || !Number.isSafeInteger(selectedServiceID) || selectedServiceID <= 0 || (scope === "routes" && selectedRouteIDs.length === 0)) {
      setError(t("invalidGrant"));
      return;
    }
    setError(undefined);
    try {
      await replace.mutateAsync({ principal_type: principalType, principal_id: selectedPrincipalID, api_service_id: selectedServiceID, scope, route_ids: scope === "routes" ? selectedRouteIDs : [] });
      onOpenChange(false);
    } catch (reason) { setError(reason instanceof Error ? reason.message : t("mutationFailed")); }
  };

  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent><DialogHeader><DialogTitle>{grant ? t("editGrant") : t("createGrant")}</DialogTitle><DialogDescription>{t("grantDialogDescription")}</DialogDescription></DialogHeader><form className="flex flex-col gap-5" onSubmit={submit}>{error ? <Alert variant="destructive"><AlertTitle>{t("mutationFailed")}</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}<FieldGroup className="gap-5"><Field><FieldLabel htmlFor="api-grant-principal-type">{t("principalType")}</FieldLabel><Select value={principalType} disabled={isEditing} onValueChange={(value) => { setPrincipalType(value as APIPrincipalType); setPrincipalID(""); }}><SelectTrigger id="api-grant-principal-type"><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{principalTypes.map((value) => <SelectItem key={value} value={value}>{t(`principalTypeOptions.${value}`)}</SelectItem>)}</SelectGroup></SelectContent></Select></Field><Field><FieldLabel htmlFor="api-grant-principal">{t("principal")}</FieldLabel><EntityPicker key={principalEntities[principalType]} id="api-grant-principal" entity={principalEntities[principalType]} value={principalID} disabled={isEditing} onChange={setPrincipalID} />{principalType === "token" ? <FieldDescription>{t("explicitTokenOnly")}</FieldDescription> : null}</Field><Field><FieldLabel htmlFor="api-grant-service">{t("service")}</FieldLabel><EntityPicker id="api-grant-service" entity="api-service" value={serviceID} disabled={isEditing} onChange={(value) => { setServiceID(value); setRouteIDs([]); }} /></Field><Field><FieldLabel>{t("scope")}</FieldLabel><ToggleGroup type="single" value={scope} onValueChange={(value) => { if (value === "service" || value === "routes") setScope(value); }}><ToggleGroupItem value="service">{t("serviceScope")}</ToggleGroupItem><ToggleGroupItem value="routes">{t("routeScope")}</ToggleGroupItem></ToggleGroup></Field>{scope === "routes" ? <Field><FieldLabel>{t("resourceOptions.api_route")}</FieldLabel><EntityMultiPicker entity="api-route" apiServiceId={selectedServiceID} value={routeIDs} disabled={!selectedServiceID} onChange={setRouteIDs} /></Field> : null}</FieldGroup><DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("cancel")}</Button><Button type="submit" disabled={replace.isPending}>{t("save")}</Button></DialogFooter></form></DialogContent></Dialog>;
}
