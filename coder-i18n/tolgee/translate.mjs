/**
 * Tolgee LLM translation orchestrator (factory step `i18n:translate`).
 *
 * Replaces the ad-hoc curl dance with one idempotent, re-runnable script that
 * mirrors the proven atlassian-i18n-toolkit runbook (Stages 3/5/6):
 *
 *   3. set project description (glossary/style) from ai-context.coder.json
 *   5. ensure a custom prompt exists and is BOUND as the default MT service
 *      (the unbound-prompt → `multiple_items_in_chunk_failed` gotcha)
 *   6. one batch over ALL keys -> target language, poll to completion
 *
 * Sensitive ops need the admin JWT (PAT can't touch description/prompts/MT
 * settings). Config via env, all with sane local defaults.
 */
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));

const URL = process.env.TOLGEE_API_URL ?? "http://localhost:8085";
const PROJECT = process.env.TOLGEE_PROJECT_ID ?? "8";
const USER = process.env.TOLGEE_ADMIN_USER ?? "admin";
const PASS = process.env.TOLGEE_ADMIN_PASSWORD ?? "admin";
const PROVIDER = process.env.TOLGEE_LLM_PROVIDER ?? "openai-gpt-5.4-mini";
const TARGET = process.env.TOLGEE_TARGET_LANG ?? "ru";
const CONTEXT = resolve(HERE, "ai-context.coder.json");

const die = (msg) => {
	console.error(`✗ ${msg}`);
	process.exit(1);
};

const jwt = async () => {
	const r = await fetch(`${URL}/api/public/generatetoken`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ username: USER, password: PASS }),
	});
	if (!r.ok) die(`generatetoken failed: ${r.status}`);
	return (await r.json()).accessToken;
};

const api = async (token, path, init = {}) => {
	const r = await fetch(`${URL}/v2/projects/${PROJECT}${path}`, {
		...init,
		headers: {
			Authorization: `Bearer ${token}`,
			"Content-Type": "application/json",
			...(init.headers ?? {}),
		},
	});
	return r;
};

const main = async () => {
	let token = await jwt();
	const ctx = JSON.parse(readFileSync(CONTEXT, "utf8"));

	// --- discover project + languages ---
	const proj = await (await api(token, "")).json();
	const langs = (await (await api(token, "/languages")).json())._embedded
		.languages;
	const target = langs.find((l) => l.tag === TARGET);
	if (!target) die(`target language '${TARGET}' not found in project ${PROJECT}`);
	const baseId = proj.baseLanguage?.id ?? langs.find((l) => l.base)?.id;

	// --- Stage 3: project description (glossary) ---
	const desc = ctx.projectDescription.slice(0, 1990);
	const put = await api(token, "", {
		method: "PUT",
		body: JSON.stringify({
			name: proj.name,
			baseLanguageId: baseId,
			description: desc,
			useNamespaces: false,
		}),
	});
	console.log(`Stage 3  project description (${desc.length} chars): http ${put.status}`);

	// --- Stage 5: ensure prompt + bind ---
	const prompts = (await (await api(token, "/prompts")).json())._embedded
		?.prompts ?? [];
	let prompt = prompts.find((p) => p.name === "translate");
	if (!prompt) {
		const created = await api(token, "/prompts", {
			method: "POST",
			body: JSON.stringify({
				name: "translate",
				providerName: PROVIDER,
				template: null,
				basicPromptOptions: [
					"KEY_NAME",
					"KEY_DESCRIPTION",
					"KEY_CONTEXT",
					"LANGUAGE_NOTES",
					"PROJECT_DESCRIPTION",
					"TM_SUGGESTIONS",
					"GLOSSARY",
					"SCREENSHOT",
				],
			}),
		});
		prompt = await created.json();
	}
	const bind = await api(
		token,
		`/machine-translation-service-settings/set-default-prompt/${prompt.id}`,
		{ method: "PUT" },
	);
	console.log(`Stage 5  prompt id=${prompt.id} bound as default MT: http ${bind.status}`);

	// --- Stage 6: collect ALL key ids (page cap 2000) ---
	const ids = [];
	let page = 0;
	let totalPages = 1;
	do {
		const data = await (await api(token, `/keys?size=2000&page=${page}`)).json();
		for (const k of data._embedded?.keys ?? []) ids.push(k.id);
		totalPages = data.page?.totalPages ?? 1;
		page++;
	} while (page < totalPages);
	console.log(`Stage 6  collected ${ids.length} key ids`);

	// one batch over all keys (Tolgee chunks internally; no llmPrompt, no client chunking)
	const job = await api(token, "/start-batch-job/machine-translate", {
		method: "POST",
		body: JSON.stringify({ keyIds: ids, targetLanguageIds: [target.id] }),
	});
	const jobBody = await job.json();
	const jobId = jobBody.id;
	if (!jobId) die(`batch launch failed: ${JSON.stringify(jobBody).slice(0, 300)}`);
	console.log(`Stage 6  batch job ${jobId} launched (${ids.length} keys -> ${TARGET})`);

	// poll to completion (re-fetch JWT — it expires on long runs)
	for (;;) {
		token = await jwt();
		const st = await (await api(token, `/batch-jobs/${jobId}`)).json();
		const line = `${st.status} ${st.progress ?? 0}/${st.totalItems ?? "?"}`;
		console.log(`         ${line}`);
		if (/^(SUCCESS|FAILED|CANCELLED)/.test(st.status)) {
			if (st.status !== "SUCCESS") die(`batch ended ${st.status}`);
			break;
		}
		await new Promise((r) => setTimeout(r, 10000));
	}
	console.log("✓ translation complete");
};

main().catch((e) => die(e.message));
