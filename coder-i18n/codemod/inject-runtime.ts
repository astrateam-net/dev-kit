/**
 * Injects the Tolgee runtime glue into the (already codemod-wrapped) Coder
 * checkout. Idempotent. All moves are reversible via `git checkout`:
 *
 *   1. copy runtime/tolgee.tsx + LanguageSwitcher.tsx -> site/src/i18n/
 *   2. copy dist/{en,ru}.json                         -> site/src/i18n/locales/
 *   3. wrap AppProviders' returned tree                in <I18nProvider>…</I18nProvider>
 *   4. add <LanguageSection/> to the top of User Settings → Appearance (best-effort)
 *
 * Run after codemod/wrap.ts. Together they turn a pristine checkout into a
 * fully wired, Russian-by-default frontend with zero hand edits in the fork.
 */
import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { IndentationText, Node, type Project, SyntaxKind, Project as TsProject } from "ts-morph";

const HERE = dirname(fileURLToPath(import.meta.url));
const MODULE = resolve(HERE, "..");
const SITE = resolve(MODULE, ".upstream/coder/site");
const I18N_DIR = `${SITE}/src/i18n`;

// 1) + 2) copy provider, switcher, and baked catalogs into the site tree
mkdirSync(`${I18N_DIR}/locales`, { recursive: true });
copyFileSync(`${MODULE}/runtime/tolgee.tsx`, `${I18N_DIR}/tolgee.tsx`);
copyFileSync(`${MODULE}/runtime/LanguageSwitcher.tsx`, `${I18N_DIR}/LanguageSwitcher.tsx`);
copyFileSync(`${MODULE}/dist/en.json`, `${I18N_DIR}/locales/en.json`);
copyFileSync(`${MODULE}/dist/ru.json`, `${I18N_DIR}/locales/ru.json`);
console.log("✓ copied tolgee.tsx + LanguageSwitcher.tsx + locales into site/src/i18n");

const project = new TsProject({
	tsConfigFilePath: `${SITE}/tsconfig.json`,
	skipAddingFilesFromTsConfig: true,
	manipulationSettings: { indentationText: IndentationText.Tab },
});

/** The top-level JSX a named arrow component returns (unwrapping `return ( … )`). */
const returnedJsx = (project: Project, file: string, component: string) => {
	const sf = project.addSourceFileAtPath(file);
	const arrow = sf
		.getVariableDeclaration(component)
		?.getInitializerIfKind(SyntaxKind.ArrowFunction);
	const body = arrow?.getBody();
	if (!body || !Node.isBlock(body)) return { sf, node: null };
	for (const stmt of body.getStatements()) {
		if (!Node.isReturnStatement(stmt)) continue;
		const expr = stmt.getExpression();
		const inner = expr && Node.isParenthesizedExpression(expr) ? expr.getExpression() : expr;
		if (
			inner &&
			(Node.isJsxElement(inner) || Node.isJsxFragment(inner) || Node.isJsxSelfClosingElement(inner))
		) {
			return { sf, node: inner };
		}
	}
	return { sf, node: null };
};

// 3) wrap AppProviders' returned tree in <I18nProvider>
{
	const { sf, node } = returnedJsx(project, `${SITE}/src/App.tsx`, "AppProviders");
	if (sf.getImportDeclaration((d) => d.getModuleSpecifierValue() === "./i18n/tolgee")) {
		console.log("✓ App.tsx already wired");
	} else if (!node) {
		throw new Error("AppProviders return not found in App.tsx");
	} else {
		node.replaceWithText(`<I18nProvider>\n${node.getText()}\n</I18nProvider>`);
		sf.addImportDeclaration({
			moduleSpecifier: "./i18n/tolgee",
			namedImports: ["I18nProvider"],
		});
		sf.saveSync();
		console.log("✓ wrapped AppProviders in <I18nProvider>");
	}
}

// 4) language switcher at the top of User Settings → Appearance (best-effort:
//    a Coder bump that renames AppearanceForm just skips this — i18n still works)
{
	const file = `${SITE}/src/pages/UserSettingsPage/AppearancePage/AppearanceForm.tsx`;
	const { sf, node } = returnedJsx(project, file, "AppearanceForm");
	if (sf.getImportDeclaration((d) => d.getModuleSpecifierValue() === "#/i18n/LanguageSwitcher")) {
		console.log("✓ AppearanceForm already has the language switcher");
	} else if (!node) {
		console.warn("⚠ AppearanceForm return not found — switcher skipped (default ru still applies)");
	} else {
		node.replaceWithText(`<>\n<LanguageSection />\n${node.getText()}\n</>`);
		sf.addImportDeclaration({
			moduleSpecifier: "#/i18n/LanguageSwitcher",
			namedImports: ["LanguageSection"],
		});
		sf.saveSync();
		console.log("✓ injected <LanguageSection /> at the top of AppearanceForm");
	}
}
