const credentials = { username: "admin", password: "chart-e2e-password-strong" };
const routes = [
  "/dashboard",
  "/billing",
  "/byok",
  "/monitoring",
  "/monitoring/insight?type=agent&id=fixture-agent-1",
  "/system",
] as const;

export default async function globalSetup() {
  const login = await fetch("http://localhost:8140/api/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(credentials),
    signal: AbortSignal.timeout(30_000),
  });
  if (!login.ok) throw new Error(`E2E prewarm login failed: ${login.status}`);
  const { token } = await login.json() as { token: string };
  for (const route of routes) {
    const response = await fetch(`http://localhost:8141${route}`, {
      headers: { cookie: `token=${token}` },
      redirect: "follow",
      signal: AbortSignal.timeout(120_000),
    });
    if (!response.ok) throw new Error(`E2E route prewarm failed: ${route} returned ${response.status}`);
    await response.arrayBuffer();
  }
}
