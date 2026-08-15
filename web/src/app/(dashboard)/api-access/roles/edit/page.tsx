"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { PageLayout } from "@/components/layout/page-layout";
import { RoleForm, RoleFormPageSkeleton } from "../../_components/role-form";

function readPositiveID(raw: string | null) { const value = Number(raw); return Number.isSafeInteger(value) && value > 0 ? value : undefined; }
function EditRoleContent() { const t = useTranslations("apiAccess"); const id = readPositiveID(useSearchParams().get("id")); return id ? <RoleForm mode={{ kind: "edit", id }} /> : <PageLayout title={t("editRole")}><Alert><AlertTitle>{t("roleNotFound")}</AlertTitle><AlertDescription>{t("roleNotFoundDescription")}</AlertDescription></Alert></PageLayout>; }
export default function EditAPIRolePage() { return <Suspense fallback={<RoleFormPageSkeleton mode="edit" />}><EditRoleContent /></Suspense>; }
