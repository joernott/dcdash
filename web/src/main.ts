// SPDX-License-Identifier: GPL-3.0-or-later
import './assets/main.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import { setUnauthorizedHandler } from './api/client'
import { createAppI18n } from './i18n'
import { createAppRouter } from './router'
import { initialLocale, usePreferencesStore } from './stores/preferences'
import { useSessionStore } from './stores/session'
import { useToastStore } from './stores/toast'

const i18n = createAppI18n(initialLocale())
const router = createAppRouter()
const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)

usePreferencesStore().init((locale) => {
  i18n.global.locale.value = locale
})

// An API request answered with "not logged in" while the user was logged in
// means that the session expired or was terminated: go back to the login page.
const session = useSessionStore()
setUnauthorizedHandler((code) => {
  if (code !== 'not_authenticated' || !session.authenticated) return
  session.expire()
  useToastStore().info(i18n.global.t('errors.session_expired'))
  const current = router.currentRoute.value
  void router.push({
    name: 'login',
    query: current.fullPath === '/' ? {} : { redirect: current.fullPath },
  })
})

app.mount('#app')
