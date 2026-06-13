/**
 * Tolgee runtime glue — injected into the Coder site as `src/i18n/tolgee.tsx`
 * by the coder-i18n factory (codemod/inject-runtime.ts). This is the ONLY
 * hand-written runtime file the fork carries; everything else is generated.
 *
 * Catalogs are baked as `staticData` (lazy per-language chunks), so the running
 * app never contacts a Tolgee server — no apiKey here, DevTools is stripped by
 * NODE_ENV in production builds. English is the source of truth and fallback;
 * Russian is the default for staff. Language is remembered in localStorage.
 */
import { FormatIcu } from "@tolgee/format-icu";
import { DevTools, Tolgee, TolgeeProvider } from "@tolgee/react";
import type { FC, ReactNode } from "react";

const STORAGE_KEY = "coder-language";

export const getLanguage = (): string =>
	localStorage.getItem(STORAGE_KEY) ?? "ru";

export const setLanguage = (lang: string): void => {
	localStorage.setItem(STORAGE_KEY, lang);
	window.location.reload();
};

export const tolgee = Tolgee()
	.use(DevTools())
	.use(FormatIcu())
	.init({
		language: getLanguage(),
		fallbackLanguage: "en",
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
