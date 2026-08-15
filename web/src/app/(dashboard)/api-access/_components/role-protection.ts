import type { APIRole } from "@/lib/api/api-access";

export function isProtectedAPIRole(role: APIRole) {
  return Boolean(role.built_in) || role.key === "gateway_admin";
}
