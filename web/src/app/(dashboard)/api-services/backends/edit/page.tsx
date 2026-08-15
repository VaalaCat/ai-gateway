"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, readPositiveID } from "../../_components/form-entry";
import { APIBackendForm } from "../../_components/backend-form";
import { routeReturnContext } from "../../_components/route-return";

function EditBackendContent() { const params = useSearchParams(); const id = readPositiveID(params.get("id")); const serviceId = readPositiveID(params.get("service_id")); const returnRoute = routeReturnContext(params); return id && serviceId ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey="editTarget" descriptionKey="targetFormDescription"><APIBackendForm mode={{ kind: "edit", id, serviceId, ...(returnRoute ? { returnRoute } : {}) }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="editTarget" subjectKey="targetNotFound" />; }
export default function EditBackendPage() { return <Suspense fallback={<FormPageSkeleton titleKey="editTarget" descriptionKey="targetFormDescription" />}><EditBackendContent /></Suspense>; }
