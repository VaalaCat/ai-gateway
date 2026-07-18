"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { RoutingForm } from "@/components/model-routing/routing-form";
import { useAuth } from "@/lib/auth";

export default function NewPage() {
  return (
    <Suspense fallback={<div className="text-muted-foreground">Loading...</div>}>
      <Inner />
    </Suspense>
  );
}

function Inner() {
  const params = useSearchParams();
  const { isAdmin } = useAuth();
  const rawTokenID = params.get("token_id");
  const tokenId = rawTokenID === null ? undefined : Number(rawTokenID);
  if (tokenId !== undefined && (!Number.isFinite(tokenId) || tokenId <= 0)) {
    return <div className="text-destructive">Invalid token_id</div>;
  }
  return (
    <RoutingForm
      mode={{ kind: "new" }}
      apiMode={tokenId === undefined || isAdmin ? "admin" : "user"}
      tokenId={tokenId}
    />
  );
}
