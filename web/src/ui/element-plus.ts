import ElementPlus from 'element-plus'

import type { App } from 'vue'

// 样式
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'

export function setupElementPlus(app: App) {
    app.use(ElementPlus)
}
