"use client";

import { useUser } from "@/lib/api/users";
import { cn } from "@/lib/utils";

interface UsernameCellProps {
  userId: number | undefined | null;
  className?: string;
}

export function UsernameCell({ userId, className }: UsernameCellProps) {
  const id = userId ?? 0;
  const { data, isLoading, isError } = useUser(id);

  if (!id) {
    return <span className={cn("text-muted-foreground", className)}>-</span>;
  }

  if (isLoading || isError || !data?.username) {
    return <span className={cn("text-muted-foreground", className)}>#{id}</span>;
  }

  return (
    <span className={className}>
      {data.username}
      <span className="text-muted-foreground ml-1">#{id}</span>
    </span>
  );
}
