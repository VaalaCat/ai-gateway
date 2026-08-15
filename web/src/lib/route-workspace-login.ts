interface LoginResponse {
  ok(): boolean;
  status(): number;
  statusText(): string;
  json(): Promise<unknown>;
}

export async function readRouteWorkspaceLoginToken(response: LoginResponse) {
  if (!response.ok()) {
    throw new Error(`Route workspace login failed (${response.status()} ${response.statusText()})`);
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new Error("Route workspace login returned an invalid response");
  }
  if (typeof body !== "object" || body === null || !("token" in body) || typeof body.token !== "string" || !body.token) {
    throw new Error("Route workspace login returned an invalid response");
  }
  return body.token;
}
