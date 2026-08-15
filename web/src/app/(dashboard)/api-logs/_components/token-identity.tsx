import { cn } from "@/lib/utils";

interface APILogTokenIdentityProps {
  tokenID: number;
  tokenName: string;
  className?: string;
}

export function APILogTokenIdentity({ tokenID, tokenName, className }: APILogTokenIdentityProps) {
  const tokenIDLabel = `#${tokenID}`;
  return (
    <span className={cn("block truncate", className)} title={tokenIDLabel}>
      {tokenName || tokenIDLabel}
    </span>
  );
}
