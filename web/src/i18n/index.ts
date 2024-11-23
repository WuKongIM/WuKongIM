import { getBrowserLang } from '@/utils'

import { createI18n } from 'vue-i18n'

import zh from './modules/zh'
import en from './modules/en'

const i18n = createI18n({
    allowComposition: true,
    legacy: false,
    locale: getBrowserLang(),
    messages: {
        zh,
        en
    }
})

export default i18n
