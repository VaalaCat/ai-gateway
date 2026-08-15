"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, readPositiveID } from "../../_components/form-entry";
import { APIBackendForm } from "../../_components/backend-form";
import { routeReturnContext } from "../../_components/route-return";

function NewBackendContent() { const params = useSearchParams(); const serviceId = readPositiveID(params.get("service_id")); const returnRoute = routeReturnContext(params); return serviceId ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey="createTarget" descriptionKey="targetFormDescription"><APIBackendForm mode={{ kind: "create", serviceId, ...(returnRoute ? { returnRoute } : {}) }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="createTarget" subjectKey="serviceNotFound" />; }
export default function NewBackendPage() { return <Suspense fallback={<FormPageSkeleton titleKey="createTarget" descriptionKey="targetFormDescription" />}><NewBackendContent /></Suspense>; }
