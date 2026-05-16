"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { RoutingForm } from "@/components/model-routing/routing-form";

export default function Page() {
  return (
    <Suspense fallback={<div className="text-muted-foreground">Loading...</div>}>
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const params = useSearchParams();
  const raw = params.get("id");
  const id = raw === null ? NaN : Number(raw);
  if (!Number.isFinite(id) || id <= 0) {
    return <div className="text-destructive">Invalid id</div>;
  }
  return <RoutingForm mode={{ kind: "edit", id }} apiMode="user" />;
}
