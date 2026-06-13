/**
 * coder-i18n codemod — wraps user-facing string literals in the Coder `site/`
 * source with Tolgee's parser-recognized shapes so `tolgee extract`/`sync` can
 * harvest every key natively. This is the contract's "wrap" mechanism: it runs
 * at build time against a throwaway Coder checkout, never committed into the fork.
 *
 *   - JSX text node            ->  <T keyName="ns.key" defaultValue="text" />
 *   - mixed text + <strong>/<i> ->  <T keyName params={{ strong: <strong/> }}
 *                                       defaultValue="a <strong>b</strong> c" />
 *   - user-facing attribute     ->  prop={t("ns.key", "text")}   (+ useTranslate hook)
 *
 * Anything it cannot wrap safely is left untouched and listed in the report, so
 * the manual remainder is explicit rather than silently dropped.
 */
import { writeFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
	type JsxAttribute,
	type JsxElement,
	Node,
	Project,
	type SourceFile,
	SyntaxKind,
} from "ts-morph";

// --- configuration ----------------------------------------------------------

const HERE = dirname(fileURLToPath(import.meta.url));
const CODER = resolve(HERE, "../.upstream/coder");
const SITE_SRC = `${CODER}/site/src`;

/** Restrict the run to these globs (passed as CLI args), else the default set. */
const argGlobs = process.argv.slice(2).filter((a) => !a.startsWith("-"));
const DEFAULT_GLOBS = [`${SITE_SRC}/pages/**/*.tsx`, `${SITE_SRC}/modules/**/*.tsx`];
const GLOBS = argGlobs.length
	? argGlobs.map((g) => (g.startsWith("/") ? g : `${SITE_SRC}/${g}`))
	: DEFAULT_GLOBS;

/** Inline formatting tags we can serialize into a defaultValue with no attrs. */
const INLINE_TAGS = new Set(["strong", "b", "i", "em", "code", "span", "u", "small", "mark"]);

/** JSX attributes whose string value is user-facing copy worth translating. */
const TEXT_ATTRS = new Set([
	"label",
	"placeholder",
	"title",
	"helperText",
	"description",
	"tooltip",
	"header",
	"subtitle",
	"message",
	"aria-label",
	"content",
	"emptyMessage",
	"confirmText",
	"cancelText",
	"confirmLabel",
	"cancelLabel",
]);

/** Skip strings that are clearly not prose. */
const looksTechnical = (s: string): boolean => {
	const t = s.trim();
	if (t.length < 2) return true;
	if (!/[A-Za-z]/.test(t)) return true; // no letters: numbers, symbols
	if (/^[A-Z0-9_]+$/.test(t)) return true; // SCREAMING_CONST
	if (/^[a-z][a-zA-Z0-9]*$/.test(t) && !t.includes(" ")) return true; // single identifier
	if (/^(https?:\/\/|\/|\.\/|#|data:|mailto:)/.test(t)) return true; // url/path
	if (/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(t)) return true; // dotted.token / domain
	if (/^\{.*\}$/.test(t)) return true; // pure ICU/expr
	return false;
};

// --- key generation ---------------------------------------------------------

const usedKeys = new Map<string, string>(); // fullKey -> defaultValue

const camelWords = (s: string): string =>
	s
		.replace(/<[^>]+>/g, " ") // strip tags
		.replace(/\{[^}]*\}/g, " ") // strip ICU
		.replace(/[^A-Za-z0-9 ]/g, " ")
		.trim()
		.split(/\s+/)
		.slice(0, 5)
		.map((w, i) =>
			i === 0 ? w.toLowerCase() : w.charAt(0).toUpperCase() + w.slice(1).toLowerCase(),
		)
		.join("")
		.slice(0, 40) || "text";

const nsForFile = (filePath: string): string => {
	const rel = relative(SITE_SRC, filePath);
	const m = rel.match(/^(pages|modules)\/([^/]+)/);
	if (!m) return "common";
	const seg = m[2].replace(/Page$/, "");
	return seg.charAt(0).toLowerCase() + seg.slice(1);
};

const makeKey = (ns: string, text: string): string => {
	const base = `${ns}.${camelWords(text)}`;
	const existing = usedKeys.get(base);
	if (existing === undefined || existing === text) {
		usedKeys.set(base, text);
		return base;
	}
	// collision with a different default — suffix until free
	for (let i = 2; ; i++) {
		const cand = `${base}_${i}`;
		const e = usedKeys.get(cand);
		if (e === undefined || e === text) {
			usedKeys.set(cand, text);
			return cand;
		}
	}
};

// --- text helpers -----------------------------------------------------------

/** Collapse JSX whitespace the way the renderer does. */
const collapse = (s: string): string => s.replace(/\s+/g, " ").trim();

/** Quote a value for a defaultValue="..." attribute, picking a safe quote. */
const attrLit = (s: string): string => (s.includes('"') ? `'${s.replace(/'/g, "\\'")}'` : `"${s}"`);

// --- report -----------------------------------------------------------------

type Skip = { file: string; line: number; reason: string; text: string };
const report = {
	files: 0,
	changedFiles: 0,
	tWrap: 0,
	tagWrap: 0,
	attrWrap: 0,
	skips: [] as Skip[],
};
const skip = (n: Node, reason: string, text: string) =>
	report.skips.push({
		file: relative(CODER, n.getSourceFile().getFilePath()),
		line: n.getStartLineNumber(),
		reason,
		text: collapse(text).slice(0, 60),
	});

// --- per-file transform -----------------------------------------------------

const ensureImport = (sf: SourceFile, names: string[]) => {
	const want = new Set(names);
	const existing = sf.getImportDeclaration((d) => d.getModuleSpecifierValue() === "@tolgee/react");
	if (existing) {
		const have = new Set(existing.getNamedImports().map((n) => n.getName()));
		for (const n of want) if (!have.has(n)) existing.addNamedImport(n);
		return;
	}
	sf.addImportDeclaration({
		moduleSpecifier: "@tolgee/react",
		namedImports: [...want],
	});
};

/** Inject `const { t } = useTranslate();` into the component that owns `node`. */
const ensureHook = (node: Node): boolean => {
	// nearest ancestor function with a block body that returns JSX
	let fn: Node | undefined = node.getParent();
	while (fn) {
		if (
			Node.isFunctionDeclaration(fn) ||
			Node.isArrowFunction(fn) ||
			Node.isFunctionExpression(fn)
		) {
			const body = fn.getBody?.();
			if (body && Node.isBlock(body)) {
				const txt = body.getText();
				if (/\buseTranslate\b/.test(txt)) return true; // already there
				body.insertStatements(0, "const { t } = useTranslate();");
				return true;
			}
		}
		fn = fn.getParent();
	}
	return false;
};

const serializeTagged = (parent: JsxElement): { value: string; params: string[] } | null => {
	const params = new Set<string>();
	let out = "";
	for (const child of parent.getJsxChildren()) {
		if (Node.isJsxText(child)) {
			out += child.getText();
		} else if (Node.isJsxElement(child)) {
			const tag = child.getOpeningElement().getTagNameNode().getText();
			if (!INLINE_TAGS.has(tag)) return null;
			if (child.getOpeningElement().getAttributes().length) return null; // attrs -> skip
			const inner = child
				.getJsxChildren()
				.map((c) => c.getText())
				.join("");
			if (/[<{]/.test(inner)) return null; // nested elements/expr -> skip
			out += `<${tag}>${inner}</${tag}>`;
			params.add(tag);
		} else if (Node.isJsxExpression(child)) {
			return null; // {expr} in mixed content -> skip (needs ICU)
		}
	}
	const value = collapse(out);
	if (!/[A-Za-z]/.test(value) || params.size === 0) return null;
	return { value, params: [...params] };
};

/**
 * True if `parent`'s component types its `children` as `string` (e.g. <Shimmer>,
 * <SectionTitle>) — wrapping the text in a <T/> ReactElement would break the
 * type (TS2745). Host tags (lowercase) always accept ReactNode, so skip the
 * (costly) type lookup for them.
 */
const wantsStringChildren = (parent: JsxElement): boolean => {
	const tag = parent.getOpeningElement().getTagNameNode();
	if (!/^[A-Z]/.test(tag.getText())) return false;
	try {
		const t = tag.getType();
		const sig = t.getCallSignatures()[0] ?? t.getConstructSignatures()[0];
		const propsParam = sig?.getParameters()[0];
		if (!propsParam) return false;
		const children = propsParam.getTypeAtLocation(tag).getProperty("children");
		if (!children) return false;
		const ct = children.getTypeAtLocation(tag).getText();
		return (
			/\bstring\b/.test(ct) &&
			!/(ReactNode|ReactElement|JSX\.Element|\bElement\b|ReactChild)/.test(ct)
		);
	} catch {
		return false;
	}
};

const transformFile = (sf: SourceFile): boolean => {
	const filePath = sf.getFilePath();
	const ns = nsForFile(filePath);
	let changed = false;
	let needT = false;
	let needTHook: Node | null = null;
	const handledText = new Set<Node>();

	// 1) mixed text + inline tags -> single <T params>
	for (const el of sf.getDescendantsOfKind(SyntaxKind.JsxElement)) {
		if (el.wasForgotten()) continue;
		const kids = el.getJsxChildren();
		const hasText = kids.some((c) => Node.isJsxText(c) && /[A-Za-z]/.test(c.getText()));
		const hasTag = kids.some(
			(c) =>
				Node.isJsxElement(c) && INLINE_TAGS.has(c.getOpeningElement().getTagNameNode().getText()),
		);
		if (!hasText || !hasTag) continue;
		if (wantsStringChildren(el)) {
			skip(el, "string-children-component", el.getText());
			continue;
		}
		const ser = serializeTagged(el);
		if (!ser) {
			skip(el, "mixed-content-complex", el.getText());
			continue;
		}
		const key = makeKey(ns, ser.value);
		const paramsObj = ser.params.map((p) => `${p}: <${p}/>`).join(", ");
		const tNode = `<T keyName=${attrLit(key)} params={{ ${paramsObj} }} defaultValue=${attrLit(ser.value)} />`;
		for (const c of el.getJsxChildren()) handledText.add(c);
		el.setBodyText(tNode); // replace all children with the single <T/>
		report.tagWrap++;
		changed = true;
		ensureImport(sf, ["T"]);
	}

	// 2) plain JSX text -> <T/>
	for (const txt of sf.getDescendantsOfKind(SyntaxKind.JsxText)) {
		if (txt.wasForgotten() || handledText.has(txt)) continue;
		const raw = txt.getText();
		const core = collapse(raw);
		if (looksTechnical(core)) continue;
		const parentEl = txt.getParentIfKind(SyntaxKind.JsxElement);
		if (parentEl && wantsStringChildren(parentEl)) {
			skip(txt, "string-children-component", core);
			continue;
		}
		// preserve surrounding whitespace
		const lead = raw.match(/^\s*/)?.[0] ?? "";
		const trail = raw.match(/\s*$/)?.[0] ?? "";
		const key = makeKey(ns, core);
		txt.replaceWithText(
			`${lead}<T keyName=${attrLit(key)} defaultValue=${attrLit(core)} />${trail}`,
		);
		report.tWrap++;
		changed = true;
		ensureImport(sf, ["T"]);
	}

	// 3) user-facing string attributes -> t("key","text")
	for (const attr of sf.getDescendantsOfKind(SyntaxKind.JsxAttribute)) {
		if (attr.wasForgotten()) continue;
		const a = attr as JsxAttribute;
		const name = a.getNameNode().getText();
		if (!TEXT_ATTRS.has(name)) continue;
		const init = a.getInitializer();
		if (!init || !Node.isStringLiteral(init)) continue;
		const val = init.getLiteralText();
		if (looksTechnical(val)) continue;
		if (!ensureHook(a)) {
			skip(a, "no-block-component-for-hook", val);
			continue;
		}
		const key = makeKey(ns, val);
		init.replaceWithText(`{t(${attrLit(key)}, ${attrLit(val)})}`);
		report.attrWrap++;
		needT = true;
		needTHook = a;
		changed = true;
	}
	if (needT && needTHook) ensureImport(sf, ["useTranslate"]);

	return changed;
};

// --- run --------------------------------------------------------------------

const project = new Project({
	tsConfigFilePath: `${CODER}/site/tsconfig.json`,
	skipAddingFilesFromTsConfig: true,
});
project.addSourceFilesAtPaths(GLOBS);

const files = project.getSourceFiles();
report.files = files.length;
for (const sf of files) {
	let changed = false;
	try {
		changed = transformFile(sf);
	} catch (e) {
		report.skips.push({
			file: relative(CODER, sf.getFilePath()),
			line: 0,
			reason: `EXCEPTION: ${(e as Error).message}`,
			text: "",
		});
		continue;
	}
	if (changed) {
		sf.saveSync();
		report.changedFiles++;
	}
}

// emit report
const keysOut: Record<string, string> = {};
for (const [k, v] of usedKeys) keysOut[k] = v;
writeFileSync(
	resolve(HERE, "../dist/_keys.extracted.json"),
	`${JSON.stringify(keysOut, null, "\t")}\n`,
);

const bySkip = report.skips.reduce<Record<string, number>>((m, s) => {
	const r = s.reason.startsWith("EXCEPTION") ? "EXCEPTION" : s.reason;
	m[r] = (m[r] ?? 0) + 1;
	return m;
}, {});

console.log("── coder-i18n codemod ─────────────────────────────");
console.log(`files scanned   : ${report.files}`);
console.log(`files changed   : ${report.changedFiles}`);
console.log(`<T> text wraps  : ${report.tWrap}`);
console.log(`<T> tag wraps   : ${report.tagWrap}`);
console.log(`t() attr wraps  : ${report.attrWrap}`);
console.log(`unique keys     : ${usedKeys.size}`);
console.log(`skipped         : ${report.skips.length}`);
for (const [r, n] of Object.entries(bySkip).sort((a, b) => b[1] - a[1]))
	console.log(`   ${r.padEnd(28)} ${n}`);
console.log("───────────────────────────────────────────────────");
