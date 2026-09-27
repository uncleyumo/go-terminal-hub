import { createI18n } from 'vue-i18n'
import en from './en'
import zhCN from './zh-CN'

// 只有这两种。后端 settings.json 里存的就是这两个字符串。
export type AppLocale = 'en' | 'zh-CN'

export const DEFAULT_LOCALE: AppLocale = 'en'

// 后端没做值校验（settings.json 里写 "fr" 也原样返回），
// 所以任何从外部来的值都要先过这一关，不认识的当默认值。
export function normalizeLocale(value: string | undefined): AppLocale {
  return value === 'zh-CN' ? 'zh-CN' : DEFAULT_LOCALE
}

export const i18n = createI18n({
  legacy: false,
  locale: DEFAULT_LOCALE,
  fallbackLocale: DEFAULT_LOCALE,
  messages: {
    en,
    'zh-CN': zhCN,
  },
})

export function setLocale(locale: AppLocale) {
  ;(i18n.global.locale as unknown as { value: string }).value = locale
}
