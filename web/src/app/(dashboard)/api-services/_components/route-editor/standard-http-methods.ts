export const STANDARD_ROUTE_HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"] as const;

export function isStandardRouteHTTPMethod(method: string) {
  return (STANDARD_ROUTE_HTTP_METHODS as readonly string[]).includes(method);
}
