"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, readPositiveID } from "../../_components/form-entry";
import { APIUpstreamForm } from "../../_components/upstream-form";
import { routeReturnContext } from "../../_components/route-return";

function EditUpstreamContent() { const params = useSearchParams(); const id = readPositiveID(params.get("id")); const serviceId = readPositiveID(params.get("service_id")); const returnRoute = routeReturnContext(params); return id && serviceId ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey="editUpstream" descriptionKey="upstreamFormDescription"><APIUpstreamForm mode={{ kind: "edit", id, serviceId, ...(returnRoute ? { returnRoute } : {}) }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="editUpstream" subjectKey="upstreamNotFound" />; }
export default function EditUpstreamPage() { return <Suspense fallback={<FormPageSkeleton titleKey="editUpstream" descriptionKey="upstreamFormDescription" />}><EditUpstreamContent /></Suspense>; }
