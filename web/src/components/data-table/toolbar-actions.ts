import type { ReactNode } from "react";

export interface ToolbarAction {
  label: string;
  icon?: ReactNode;
  onClick?: () => void;
  href?: string;
  variant?: "default" | "outline" | "destructive";
  disabled?: boolean;
  loading?: boolean;
}
