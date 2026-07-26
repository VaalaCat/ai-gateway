import { rmSync } from "node:fs";
import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const profiles = Object.freeze({
  normal: Object.freeze({
    distDir: ".next-e2e-normal",
    port: "8141",
    apiOrigin: "http://localhost:8140",
  }),
  degraded: Object.freeze({
    distDir: ".next-e2e-degraded",
    port: "8241",
    apiOrigin: "http://localhost:8240",
  }),
  largeMigration: Object.freeze({
    distDir: ".next-e2e-large-migration",
    port: "8341",
    apiOrigin: "http://localhost:8340",
  }),
});

const profileName = process.argv[2];
if (!Object.hasOwn(profiles, profileName)) {
  console.error("usage: node e2e/start-next-e2e.mjs <normal|degraded|largeMigration>");
  process.exit(2);
}

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const profile = profiles[profileName];
const distPath = resolve(webRoot, profile.distDir);
const allowedDistPaths = new Set(Object.values(profiles).map(({ distDir }) => resolve(webRoot, distDir)));
if (!allowedDistPaths.has(distPath)) {
  throw new Error(`refusing to clean non-E2E dist directory: ${distPath}`);
}
rmSync(distPath, { recursive: true, force: true });

const pnpm = process.platform === "win32" ? "pnpm.cmd" : "pnpm";
const childEnv = {
  ...process.env,
  AIGW_E2E_SERVER: "1",
  AIGW_E2E_DIST_DIR: profile.distDir,
  AIGW_API_ORIGIN: profile.apiOrigin,
  NEXT_PUBLIC_LOCALE: "en",
};
let activeChild;

const forwardSignal = (signal) => {
  if (activeChild?.exitCode === null && activeChild.signalCode === null) activeChild.kill(signal);
};
for (const signal of ["SIGTERM", "SIGINT"]) {
  process.on(signal, () => forwardSignal(signal));
}

const run = (args) => new Promise((resolveRun, rejectRun) => {
  const child = spawn(pnpm, args, {
    cwd: webRoot,
    env: childEnv,
    stdio: "inherit",
  });
  activeChild = child;
  child.once("error", rejectRun);
  child.once("exit", (code, signal) => resolveRun({ code, signal }));
});

const build = await run(["exec", "next", "build", "--webpack"]);
if (build.code !== 0) {
  console.error(`E2E Next build failed with ${build.signal ? `signal ${build.signal}` : `exit code ${build.code ?? 1}`}`);
  process.exit(build.code ?? 1);
}

const server = await run(["exec", "next", "start", "--port", profile.port]);
process.exit(server.code ?? (server.signal ? 1 : 0));
