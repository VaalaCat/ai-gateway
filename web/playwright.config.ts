import { defineConfig } from "@playwright/test";

import { createPlaywrightE2EConfig, createPlaywrightE2ERunProfile } from "./src/lib/playwright-e2e-config";

export default defineConfig(createPlaywrightE2EConfig(createPlaywrightE2ERunProfile(process.env)));
