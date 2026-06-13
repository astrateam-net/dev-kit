/**
 * Language switcher — injected into the Coder site as `src/i18n/LanguageSwitcher.tsx`
 * and rendered at the top of User Settings → Appearance (contract §6). Pure
 * client-side: persists via localStorage (`setLanguage` reloads), no Coder
 * backend changes. Bilingual static labels so it reads correctly in either mode.
 */
import { useTolgee } from "@tolgee/react";
import type { FC } from "react";
import { Label } from "#/components/Label/Label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "#/components/Select/Select";
import { Section } from "#/pages/UserSettingsPage/Section";

const LANGUAGES = [
	{ value: "ru", label: "Русский" },
	{ value: "en", label: "English" },
];

export const LanguageSection: FC = () => {
	// re-render on language change; changeLanguage persists via LanguageStorage
	const tolgee = useTolgee(["language"]);
	return (
		<Section title="Язык · Language" layout="fluid" className="mb-12">
			<div className="flex flex-col gap-2">
				<Label className="text-sm font-medium">
					Язык интерфейса · Interface language
				</Label>
				<Select
					value={tolgee.getLanguage()}
					onValueChange={(v) => tolgee.changeLanguage(v)}
				>
					<SelectTrigger className="w-48 text-content-primary">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{LANGUAGES.map((l) => (
							<SelectItem key={l.value} value={l.value}>
								{l.label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
		</Section>
	);
};
