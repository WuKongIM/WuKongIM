import { defineI18n } from 'fumadocs-core/i18n';
import { locales } from './navigation';

export const i18n = defineI18n({
  languages: [...locales],
  defaultLanguage: 'zh',
  fallbackLanguage: null,
  hideLocale: 'never',
  parser: 'dot',
});
