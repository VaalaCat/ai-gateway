"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, readPositiveID } from "../_components/form-entry";
import { APIServiceForm } from "../_components/service-form";

function EditAPIServiceContent() { const id = readPositiveID(useSearchParams().get("id")); return id ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId: id }} titleKey="editService" descriptionKey="serviceFormDescription"><APIServiceForm mode={{ kind: "edit", id }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="editService" subjectKey="serviceNotFound" />; }
export default function EditAPIServicePage() { return <Suspense fallback={<FormPageSkeleton titleKey="editService" descriptionKey="serviceFormDescription" />}><EditAPIServiceContent /></Suspense>; }
