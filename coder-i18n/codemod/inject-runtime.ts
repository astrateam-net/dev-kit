/**
 * Injects the Tolgee runtime glue into the (already codemod-wrapped) Coder
 * checkout. Idempotent. Three moves, all reversible via `git checkout`:
 *
 *   1. copy runtime/tolgee.tsx          -> site/src/i18n/tolgee.tsx
 *   2. copy dist/{en,ru}.json           -> site/src/i18n/locales/
 *   3. wrap AppProviders' returned tree  in <I18nProvider>…</I18nProvider>
 *
 * Run after codemod/wrap.ts. Together they turn a pristine checkout into a
 * fully wired, Russian-by-default frontend with zero hand edits in the fork.
 */
import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { IndentationText, Node, Project, SyntaxKind } from "ts-morph";

const HERE = dirname(fileURLToPath(import.meta.url));
const MODULE = resolve(HERE, "..");
const SITE = resolve(MODULE, ".upstream/coder/site");
const I18N_DIR = `${SITE}/src/i18n`;

// 1) + 2) copy provider and baked catalogs into the site tree
mkdirSync(`${I18N_DIR}/locales`, { recursive: true });
copyFileSync(`${MODULE}/runtime/tolgee.tsx`, `${I18N_DIR}/tolgee.tsx`);
copyFileSync(`${MODULE}/dist/en.json`, `${I18N_DIR}/locales/en.json`);
copyFileSync(`${MODULE}/dist/ru.json`, `${I18N_DIR}/locales/ru.json`);
console.log("✓ copied tolgee.tsx + locales/{en,ru}.json into site/src/i18n");

// 3) wrap AppProviders' returned JSX in <I18nProvider>
const project = new Project({
	tsConfigFilePath: `${SITE}/tsconfig.json`,
	skipAddingFilesFromTsConfig: true,
	manipulationSettings: { indentationText: IndentationText.Tab },
});
const app = project.addSourceFileAtPath(`${SITE}/src/App.tsx`);

const alreadyWired = app.getImportDeclaration(
	(d) => d.getModuleSpecifierValue() === "./i18n/tolgee",
);
if (alreadyWired) {
	console.log("✓ App.tsx already wired — nothing to do");
} else {
	const decl = app.getVariableDeclaration("AppProviders");
	if (!decl) throw new Error("AppProviders not found in App.tsx");
	const arrow = decl.getInitializerIfKindOrThrow(SyntaxKind.ArrowFunction);
	const body = arrow.getBody();
	if (!Node.isBlock(body)) throw new Error("AppProviders body is not a block");
	const ret = body.getStatementByKindOrThrow(SyntaxKind.ReturnStatement);
	const expr = ret.getExpressionOrThrow();
	// the returned JSX is usually parenthesized: `return ( <…/> );`
	const inner = Node.isParenthesizedExpression(expr) ? expr.getExpression() : expr;
	const jsx = inner.getText();
	// ts-morph re-indents continuation lines (with tabs, per manipulationSettings)
	inner.replaceWithText(`<I18nProvider>\n${jsx}\n</I18nProvider>`);
	app.addImportDeclaration({
		moduleSpecifier: "./i18n/tolgee",
		namedImports: ["I18nProvider"],
	});
	app.saveSync();
	console.log("✓ wrapped AppProviders in <I18nProvider> + added import");
}
