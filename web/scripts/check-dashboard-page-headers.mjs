import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ALLOWED_PAGE_HEADER = join("components", "layout", "page-header.tsx");
const MODULE_CANDIDATES = [
  ".tsx",
  ".ts",
  ".jsx",
  ".js",
  ".mjs",
  ".cjs",
  "/index.tsx",
  "/index.ts",
  "/index.jsx",
  "/index.js",
];

function isProductionTsx(filePath, srcRoot) {
  const sourcePath = relative(srcRoot, filePath);
  const pathParts = sourcePath.split(sep);
  const filename = pathParts.at(-1) ?? "";

  return (
    filename.endsWith(".tsx")
    && !filename.endsWith(".test.tsx")
    && !filename.endsWith(".spec.tsx")
    && !filename.endsWith(".fixture.tsx")
    && !pathParts.includes("__fixtures__")
    && !pathParts.includes("fixtures")
    && !pathParts.includes("fixture")
    && sourcePath !== ALLOWED_PAGE_HEADER
  );
}

function collectTsxFiles(directory, srcRoot, files) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = join(directory, entry.name);
    if (entry.isDirectory()) {
      collectTsxFiles(entryPath, srcRoot, files);
    } else if (entry.isFile() && isProductionTsx(entryPath, srcRoot)) {
      files.push(entryPath);
    }
  }
}

function collectDashboardPages(directory, pages) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = join(directory, entry.name);
    if (entry.isDirectory()) {
      collectDashboardPages(entryPath, pages);
    } else if (entry.isFile() && entry.name === "page.tsx") {
      pages.push(entryPath);
    }
  }
}

function hasModifier(node, kind) {
  return node.modifiers?.some((modifier) => modifier.kind === kind) ?? false;
}

function resolveImportedModule(specifier, fromFile, srcRoot) {
  let basePath;
  if (specifier.startsWith("@/")) {
    basePath = join(srcRoot, specifier.slice(2));
  } else if (specifier.startsWith(".")) {
    basePath = resolve(dirname(fromFile), specifier);
  } else {
    return undefined;
  }

  const candidates = extname(basePath)
    ? [basePath]
    : MODULE_CANDIDATES.map((suffix) => `${basePath}${suffix}`);
  return candidates.find((candidate) => existsSync(candidate));
}

function createHeaderReachability(srcRoot) {
  const moduleCache = new Map();
  const resultCache = new Map();
  const visiting = new Set();

  const loadModule = (filePath) => {
    const cached = moduleCache.get(filePath);
    if (cached) return cached;

    const source = readFileSync(filePath, "utf8");
    const ast = ts.createSourceFile(
      filePath,
      source,
      ts.ScriptTarget.Latest,
      true,
      filePath.endsWith("x") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );
    const context = {
      ast,
      filePath,
      imports: new Map(),
      locals: new Map(),
    };
    moduleCache.set(filePath, context);

    for (const statement of ast.statements) {
      if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
        const importClause = statement.importClause;
        if (!importClause) continue;
        const specifier = statement.moduleSpecifier.text;
        if (importClause.name) {
          context.imports.set(importClause.name.text, {
            importedName: "default",
            specifier,
          });
        }
        const bindings = importClause.namedBindings;
        if (bindings && ts.isNamedImports(bindings)) {
          for (const element of bindings.elements) {
            context.imports.set(element.name.text, {
              importedName: (element.propertyName ?? element.name).text,
              specifier,
            });
          }
        } else if (bindings && ts.isNamespaceImport(bindings)) {
          context.imports.set(bindings.name.text, {
            importedName: "*",
            specifier,
          });
        }
      }
    }

    const collectLocals = (node) => {
      if ((ts.isFunctionDeclaration(node) || ts.isClassDeclaration(node)) && node.name) {
        context.locals.set(node.name.text, node);
      } else if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
        context.locals.set(node.name.text, node);
      }
      ts.forEachChild(node, collectLocals);
    };
    collectLocals(ast);

    return context;
  };

  const withCycleGuard = (key, callback) => {
    if (resultCache.has(key)) return resultCache.get(key);
    if (visiting.has(key)) return false;

    visiting.add(key);
    const result = callback();
    visiting.delete(key);
    resultCache.set(key, result);
    return result;
  };

  const exportHasHeader = (filePath, exportName) => {
    const key = `export:${filePath}#${exportName}`;
    return withCycleGuard(key, () => {
      const context = loadModule(filePath);

      for (const statement of context.ast.statements) {
        if (exportName === "default") {
          if (
            (ts.isFunctionDeclaration(statement) || ts.isClassDeclaration(statement))
            && hasModifier(statement, ts.SyntaxKind.DefaultKeyword)
          ) {
            return nodeHasHeader(statement, context);
          }
          if (ts.isExportAssignment(statement)) {
            return nodeHasHeader(statement.expression, context, true);
          }
        }

        if (
          ts.isFunctionDeclaration(statement)
          && statement.name?.text === exportName
          && hasModifier(statement, ts.SyntaxKind.ExportKeyword)
        ) {
          return nodeHasHeader(statement, context);
        }

        if (
          ts.isVariableStatement(statement)
          && hasModifier(statement, ts.SyntaxKind.ExportKeyword)
        ) {
          for (const declaration of statement.declarationList.declarations) {
            if (ts.isIdentifier(declaration.name) && declaration.name.text === exportName) {
              return nodeHasHeader(declaration, context);
            }
          }
        }

        if (ts.isExportDeclaration(statement)) {
          const moduleSpecifier = statement.moduleSpecifier
            && ts.isStringLiteral(statement.moduleSpecifier)
            ? statement.moduleSpecifier.text
            : undefined;
          if (statement.exportClause && ts.isNamedExports(statement.exportClause)) {
            for (const element of statement.exportClause.elements) {
              if (element.name.text !== exportName) continue;
              const originalName = (element.propertyName ?? element.name).text;
              if (moduleSpecifier) {
                const importedFile = resolveImportedModule(
                  moduleSpecifier,
                  filePath,
                  srcRoot,
                );
                return importedFile
                  ? exportHasHeader(importedFile, originalName)
                  : false;
              }
              return symbolHasHeader(context, originalName);
            }
          } else if (!statement.exportClause && moduleSpecifier) {
            const importedFile = resolveImportedModule(moduleSpecifier, filePath, srcRoot);
            if (importedFile && exportHasHeader(importedFile, exportName)) return true;
          }
        }
      }

      return symbolHasHeader(context, exportName);
    });
  };

  const importedSymbolHasHeader = (context, imported, memberName) => {
    const importedFile = resolveImportedModule(
      imported.specifier,
      context.filePath,
      srcRoot,
    );
    if (!importedFile) return false;
    const exportName = imported.importedName === "*"
      ? memberName
      : imported.importedName;
    return exportName ? exportHasHeader(importedFile, exportName) : false;
  };

  const symbolHasHeader = (context, name) => {
    const local = context.locals.get(name);
    if (local) {
      const key = `local:${context.filePath}#${name}`;
      return withCycleGuard(key, () => nodeHasHeader(local, context));
    }

    const imported = context.imports.get(name);
    return imported ? importedSymbolHasHeader(context, imported) : false;
  };

  const tagHasHeader = (tagName, context) => {
    if (ts.isIdentifier(tagName)) {
      if (tagName.text === "h1") return true;
      if (tagName.text[0] === tagName.text[0]?.toLowerCase()) return false;
      return symbolHasHeader(context, tagName.text);
    }

    if (ts.isPropertyAccessExpression(tagName) && ts.isIdentifier(tagName.expression)) {
      const imported = context.imports.get(tagName.expression.text);
      return imported
        ? importedSymbolHasHeader(context, imported, tagName.name.text)
        : false;
    }

    return false;
  };

  const nodeHasHeader = (node, context, followReference = false) => {
    if (followReference && ts.isIdentifier(node)) {
      return symbolHasHeader(context, node.text);
    }

    if (ts.isJsxElement(node)) {
      if (tagHasHeader(node.openingElement.tagName, context)) return true;
      return node.children.some((child) => nodeHasHeader(child, context));
    }

    if (ts.isJsxSelfClosingElement(node)) {
      return tagHasHeader(node.tagName, context);
    }

    if (ts.isCallExpression(node)) {
      if (nodeHasHeader(node.expression, context, true)) return true;
      return node.arguments.some((argument) => nodeHasHeader(argument, context, true));
    }

    let found = false;
    ts.forEachChild(node, (child) => {
      if (!found && nodeHasHeader(child, context)) found = true;
    });
    return found;
  };

  return {
    exportHasHeader,
    loadModule,
    nodeHasHeader,
    tagHasHeader,
  };
}

function sourceFinding(context, node, message) {
  const position = context.ast.getLineAndCharacterOfPosition(node.getStart(context.ast));
  return {
    filePath: context.filePath,
    line: position.line + 1,
    column: position.character + 1,
    message,
  };
}

function findSuspenseFallbackViolations(context, reachability) {
  const findings = [];

  const isSuspense = (tagName) => {
    if (!ts.isIdentifier(tagName)) return false;
    if (tagName.text === "Suspense") return true;
    const imported = context.imports.get(tagName.text);
    return imported?.specifier === "react" && imported.importedName === "Suspense";
  };

  const hasHeaderAncestor = (node) => {
    let ancestor = node.parent;
    while (ancestor) {
      if (
        ts.isJsxElement(ancestor)
        && reachability.tagHasHeader(ancestor.openingElement.tagName, context)
      ) {
        return true;
      }
      ancestor = ancestor.parent;
    }
    return false;
  };

  const visit = (node) => {
    if (
      (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node))
      && isSuspense(node.tagName)
    ) {
      const fallback = node.attributes.properties.find(
        (property) => ts.isJsxAttribute(property) && property.name.getText(context.ast) === "fallback",
      );
      let fallbackHasHeader = false;
      if (fallback && ts.isJsxAttribute(fallback) && fallback.initializer) {
        if (ts.isJsxExpression(fallback.initializer)) {
          fallbackHasHeader = Boolean(
            fallback.initializer.expression
            && reachability.nodeHasHeader(fallback.initializer.expression, context, true),
          );
        } else {
          fallbackHasHeader = reachability.nodeHasHeader(fallback.initializer, context, true);
        }
      }

      if (!fallbackHasHeader && !hasHeaderAncestor(node)) {
        findings.push(sourceFinding(
          context,
          node,
          "Suspense fallback has 0 reachable Header/h1 consumers",
        ));
      }
    }
    ts.forEachChild(node, visit);
  };

  visit(context.ast);
  return findings;
}

export function findDirectPageHeadings(filePath, source) {
  const ast = ts.createSourceFile(
    filePath,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const findings = [];

  const visit = (node) => {
    if (
      (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node))
      && node.tagName.getText(ast) === "h1"
    ) {
      const position = ast.getLineAndCharacterOfPosition(node.getStart(ast));
      findings.push({
        filePath,
        line: position.line + 1,
        column: position.character + 1,
      });
    }
    ts.forEachChild(node, visit);
  };

  visit(ast);
  return findings;
}

export function scanPageHeadingSources(srcRoot) {
  const files = [];
  const dashboardRoot = join(srcRoot, "app", "(dashboard)");
  const componentsRoot = join(srcRoot, "components");

  collectTsxFiles(dashboardRoot, srcRoot, files);
  collectTsxFiles(componentsRoot, srcRoot, files);

  return files
    .sort()
    .flatMap((filePath) => findDirectPageHeadings(filePath, readFileSync(filePath, "utf8")));
}

export function scanDashboardPageHeaders(srcRoot) {
  const dashboardRoot = join(srcRoot, "app", "(dashboard)");
  const pages = [];
  collectDashboardPages(dashboardRoot, pages);
  pages.sort();

  const reachability = createHeaderReachability(srcRoot);
  const findings = [];
  for (const page of pages) {
    const context = reachability.loadModule(page);
    if (!reachability.exportHasHeader(page, "default")) {
      findings.push(sourceFinding(
        context,
        context.ast,
        "page has 0 reachable Header/h1 consumers",
      ));
    }
    findings.push(...findSuspenseFallbackViolations(context, reachability));
  }

  return { findings, pageCount: pages.length };
}

function runCli() {
  const srcRoot = process.argv[2] ? resolve(process.argv[2]) : resolve(SCRIPT_DIR, "../src");
  const directFindings = scanPageHeadingSources(srcRoot).map((finding) => ({
    ...finding,
    message: "direct <h1> is only allowed in PageHeader",
  }));
  const pageScan = scanDashboardPageHeaders(srcRoot);
  const findings = [...directFindings, ...pageScan.findings].sort(
    (left, right) => left.filePath.localeCompare(right.filePath)
      || left.line - right.line
      || left.column - right.column,
  );

  for (const finding of findings) {
    console.error(
      `${relative(srcRoot, finding.filePath)}:${finding.line}:${finding.column} ${finding.message}`,
    );
  }
  if (findings.length > 0) {
    process.exitCode = 1;
    return;
  }

  console.log(`Checked ${pageScan.pageCount} dashboard pages: Header coverage passed.`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runCli();
}
