import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

import {
  findDirectPageHeadings,
  scanPageHeadingSources,
} from "./check-dashboard-page-headers.mjs";

const SCRIPT_PATH = fileURLToPath(new URL("./check-dashboard-page-headers.mjs", import.meta.url));

function runFixture(files) {
  const root = mkdtempSync(join(tmpdir(), "page-header-fixture-"));
  for (const [relativePath, source] of Object.entries({
    "components/layout/page-header.tsx": "export const PageHeader = ({ title }) => <h1>{title}</h1>",
    "components/layout/page-layout.tsx": "import { PageHeader } from './page-header'; export const PageLayout = ({ title, children }) => <><PageHeader title={title} />{children}</>",
    ...files,
  })) {
    const filePath = join(root, relativePath);
    mkdirSync(join(filePath, ".."), { recursive: true });
    writeFileSync(filePath, source);
  }
  return {
    root,
    result: spawnSync(process.execPath, [SCRIPT_PATH, root], { encoding: "utf8" }),
  };
}

test("reports a JSX h1 with source position", () => {
  const [finding] = findDirectPageHeadings(
    "page.tsx",
    "export default () => <h1>Title</h1>",
  );

  assert.equal(finding.filePath, "page.tsx");
  assert.equal(finding.line, 1);
  assert.ok(finding.column > 0);
});

test("reports a self-closing JSX h1", () => {
  assert.equal(
    findDirectPageHeadings("page.tsx", "export default () => <h1 />").length,
    1,
  );
});

test("allows PageHeader and PageLayout consumers", () => {
  assert.deepEqual(
    findDirectPageHeadings(
      "page.tsx",
      "export default () => <PageHeader title='Title' />",
    ),
    [],
  );
  assert.deepEqual(
    findDirectPageHeadings(
      "page.tsx",
      "export default () => <PageLayout title='Title'>Content</PageLayout>",
    ),
    [],
  );
});

test("ignores h1 text in strings and comments", () => {
  assert.deepEqual(
    findDirectPageHeadings("page.tsx", "const sample = '<h1>'; // <h1>"),
    [],
  );
});

test("scans dashboard pages and shared components while excluding allowed and test sources", () => {
  const root = mkdtempSync(join(tmpdir(), "page-header-check-"));
  try {
    const files = {
      "app/(dashboard)/models/page.tsx": "export default () => <h1>Model title</h1>",
      "components/business/dashboard-title.tsx": "export const DashboardTitle = () => <h1>Shared title</h1>",
      "components/layout/page-header.tsx": "export const PageHeader = () => <h1>Allowed title</h1>",
      "components/business/dashboard-title.test.tsx": "export const TestTitle = () => <h1>Test title</h1>",
      "components/business/__fixtures__/dashboard-title.tsx": "export const FixtureTitle = () => <h1>Fixture title</h1>",
      "components/business/fixture/dashboard-title.tsx": "export const FixtureTitle = () => <h1>Fixture title</h1>",
      "components/business/dashboard-title.fixture.tsx": "export const FixtureTitle = () => <h1>Fixture title</h1>",
    };
    for (const [relativePath, source] of Object.entries(files)) {
      const filePath = join(root, relativePath);
      mkdirSync(join(filePath, ".."), { recursive: true });
      writeFileSync(filePath, source);
    }

    const findings = scanPageHeadingSources(root);
    assert.deepEqual(
      findings.map(({ filePath }) => relative(root, filePath)),
      [
        "app/(dashboard)/models/page.tsx",
        "components/business/dashboard-title.tsx",
      ],
    );
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
});

test("exits non-zero and reports source positions when the CLI finds a heading", () => {
  const root = mkdtempSync(join(tmpdir(), "page-header-cli-"));
  try {
    const pagePath = join(root, "app/(dashboard)/models/page.tsx");
    mkdirSync(join(pagePath, ".."), { recursive: true });
    writeFileSync(pagePath, "export default () => <h1>Model title</h1>");
    mkdirSync(join(root, "components/layout"), { recursive: true });
    writeFileSync(join(root, "components/layout/page-header.tsx"), "export const PageHeader = () => <h1>Allowed title</h1>");

    const result = spawnSync(process.execPath, [SCRIPT_PATH, root], { encoding: "utf8" });
    assert.equal(result.status, 1);
    assert.match(result.stderr.replaceAll("\\", "/"), /app\/\(dashboard\)\/models\/page\.tsx:1:\d+ direct <h1>/);
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
});

test("exits non-zero when a dashboard page has no reachable Header consumer", () => {
  const fixture = runFixture({
    "app/(dashboard)/models/page.tsx": "export default () => <main />",
  });
  try {
    assert.equal(fixture.result.status, 1);
    assert.match(
      fixture.result.stderr.replaceAll("\\", "/"),
      /app\/\(dashboard\)\/models\/page\.tsx.*0.*(?:Header|h1)/i,
    );
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});

test("accepts direct PageHeader and imported PageLayout consumers", () => {
  const fixture = runFixture({
    "components/business/form-shell.tsx": "import { PageLayout } from '@/components/layout/page-layout'; export const FormShell = () => <PageLayout title='Edit'>Form</PageLayout>",
    "app/(dashboard)/models/page.tsx": "import { PageHeader } from '@/components/layout/page-header'; export default () => <PageHeader title='Models' />",
    "app/(dashboard)/models/edit/page.tsx": "import { FormShell } from '@/components/business/form-shell'; export default () => <FormShell />",
  });
  try {
    assert.equal(fixture.result.status, 0, fixture.result.stderr);
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});

test("exits non-zero when Suspense has no fallback or its fallback has no Header", () => {
  for (const source of [
    "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; export default () => <Suspense><PageLayout title='Ready'>Ready</PageLayout></Suspense>",
    "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; export default () => <Suspense fallback={<span>Loading</span>}><PageLayout title='Ready'>Ready</PageLayout></Suspense>",
  ]) {
    const fixture = runFixture({ "app/(dashboard)/models/page.tsx": source });
    try {
      assert.equal(fixture.result.status, 1);
      assert.match(fixture.result.stderr, /Suspense.*fallback.*(?:Header|h1)/i);
    } finally {
      rmSync(fixture.root, { force: true, recursive: true });
    }
  }
});

test("accepts a Header-bearing Suspense fallback", () => {
  const fixture = runFixture({
    "app/(dashboard)/models/page.tsx": "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; export default () => <Suspense fallback={<PageLayout title='Models'>Loading</PageLayout>}><PageLayout title='Models'>Ready</PageLayout></Suspense>",
  });
  try {
    assert.equal(fixture.result.status, 0, fixture.result.stderr);
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});

test("accepts a content fallback beneath a persistent PageLayout Header", () => {
  const fixture = runFixture({
    "app/(dashboard)/models/page.tsx": "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; export default () => <PageLayout title='Models'><Suspense fallback={<span>Loading</span>}><main>Ready</main></Suspense></PageLayout>",
  });
  try {
    assert.equal(fixture.result.status, 0, fixture.result.stderr);
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});

test("resolves imported Suspense fallback components", () => {
  const passing = runFixture({
    "components/layout/page-layout-skeleton.tsx": "import { PageLayout } from './page-layout'; export const PageLayoutSkeleton = () => <PageLayout title='Loading'>Loading</PageLayout>",
    "app/(dashboard)/models/page.tsx": "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; import { PageLayoutSkeleton } from '@/components/layout/page-layout-skeleton'; export default () => <Suspense fallback={<PageLayoutSkeleton />}><PageLayout title='Models'>Ready</PageLayout></Suspense>",
  });
  try {
    assert.equal(passing.result.status, 0, passing.result.stderr);
  } finally {
    rmSync(passing.root, { force: true, recursive: true });
  }

  const failing = runFixture({
    "components/layout/spinner-fallback.tsx": "export const SpinnerFallback = () => <span>Loading</span>",
    "app/(dashboard)/models/page.tsx": "import { Suspense } from 'react'; import { PageLayout } from '@/components/layout/page-layout'; import { SpinnerFallback } from '@/components/layout/spinner-fallback'; export default () => <Suspense fallback={<SpinnerFallback />}><PageLayout title='Models'>Ready</PageLayout></Suspense>",
  });
  try {
    assert.equal(failing.result.status, 1);
    assert.match(failing.result.stderr, /Suspense.*fallback.*(?:Header|h1)/i);
  } finally {
    rmSync(failing.root, { force: true, recursive: true });
  }
});

test("terminates import cycles without inventing a reachable Header", () => {
  const fixture = runFixture({
    "components/business/cycle-a.tsx": "import { CycleB } from './cycle-b'; export const CycleA = () => <CycleB />",
    "components/business/cycle-b.tsx": "import { CycleA } from './cycle-a'; export const CycleB = () => <CycleA />",
    "app/(dashboard)/models/page.tsx": "import { CycleA } from '@/components/business/cycle-a'; export default () => <CycleA />",
  });
  try {
    assert.equal(fixture.result.status, 1);
    assert.match(fixture.result.stderr, /0.*(?:Header|h1)/i);
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});

test("resolves local named export lists without a module specifier", () => {
  const fixture = runFixture({
    "components/business/local-shell.tsx": "import { PageLayout } from '@/components/layout/page-layout'; const LocalShell = () => <PageLayout title='Local'>Content</PageLayout>; export { LocalShell }",
    "app/(dashboard)/models/page.tsx": "import { LocalShell } from '@/components/business/local-shell'; export default () => <LocalShell />",
  });
  try {
    assert.equal(fixture.result.status, 0, fixture.result.stderr);
  } finally {
    rmSync(fixture.root, { force: true, recursive: true });
  }
});
