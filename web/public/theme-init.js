// SPDX-License-Identifier: GPL-3.0-or-later
// Applies the cached theme before the page renders, so that it does not flash in
// the wrong theme. Loaded as a blocking script from index.html; it is a separate
// file because the Content-Security-Policy does not allow inline scripts.
// Keep in sync with src/stores/preferences.ts.
;(function () {
  var theme = 'system'
  try {
    theme = localStorage.getItem('dcdash.theme') || 'system'
  } catch {
    // storage not available: follow the system
  }
  var dark =
    theme === 'dark' ||
    (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
})()
