/**
 * Tolgee runtime glue — injected into the Coder site as `src/i18n/tolgee.tsx`
 * by the coder-i18n factory (codemod/inject-runtime.ts). This is the ONLY
 * hand-written runtime file the fork carries; everything else is generated.
 *
 * Catalogs are baked as `staticData` (lazy per-language chunks), so the running
 * app never contacts a Tolgee server — no apiKey here, DevTools is stripped by
 * NODE_ENV in production builds.
 *
 * Language policy (contract §6): Russian is the firm default for staff —
 * `defaultLanguage: "ru"` with NO LanguageDetector, so the browser's
 * `navigator.language` is deliberately ignored. `LanguageStorage` remembers a
 * user's switch (localStorage) across reloads. English is the fallback and the
 * source of truth (every key's defaultValue).
 */
import { FormatIcu } from "@tolgee/format-icu";
import { DevTools, LanguageStorage, Tolgee, TolgeeProvider } from "@tolgee/react";
import type { FC, ReactNode } from "react";

export const tolgee = Tolgee()
	.use(DevTools())
	.use(FormatIcu())
	.use(LanguageStorage())
	.init({
		defaultLanguage: "ru",
		fallbackLanguage: "en",
		availableLanguages: ["ru", "en"],
		// Lazy per-language chunks: an English user never downloads the RU catalog.
		staticData: {
			en: () => import("./locales/en.json").then((m) => m.default),
			ru: () => import("./locales/ru.json").then((m) => m.default),
		},
	});

export const I18nProvider: FC<{ children: ReactNode }> = ({ children }) => {
	return (
		<TolgeeProvider tolgee={tolgee} fallback={null}>
			{children}
		</TolgeeProvider>
	);
};
