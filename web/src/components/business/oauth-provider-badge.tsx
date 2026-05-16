import { cn } from "@/lib/utils";

const SIZE_CLASS = {
  sm: "size-4 text-[8px]",
  md: "size-6 text-xs",
  lg: "size-12 text-base",
} as const;

type Size = keyof typeof SIZE_CLASS;

interface OAuthProviderBadgeProps {
  displayName: string;
  iconUrl?: string;
  size?: Size;
  className?: string;
}

export function OAuthProviderBadge({
  displayName,
  iconUrl,
  size = "sm",
  className,
}: OAuthProviderBadgeProps) {
  const sizeClass = SIZE_CLASS[size];
  if (iconUrl) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={iconUrl}
        alt=""
        className={cn(sizeClass, "rounded object-cover", className)}
      />
    );
  }
  const initial = (displayName?.[0] ?? "?").toUpperCase();
  return (
    <span
      aria-hidden
      className={cn(
        sizeClass,
        "inline-flex items-center justify-center rounded bg-muted font-semibold text-muted-foreground",
        className,
      )}
    >
      {initial}
    </span>
  );
}
