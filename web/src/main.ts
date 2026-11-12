// SPDX-License-Identifier: GPL-3.0-or-later
import './assets/main.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import router from './router'
import { createAppI18n } from './i18n'

const i18n = createAppI18n()
document.documentElement.lang = i18n.global.locale.value

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)

app.mount('#app')
