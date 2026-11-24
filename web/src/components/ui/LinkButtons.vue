<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- The UI, SSH and RDP buttons of a system: the web interface opens in a new tab, SSH as an
     ssh:// link, RDP as a downloaded connection file. The default slot adds buttons before
     them, for example a shell. -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { downloadRdp } from '@/systems'

defineProps<{
  // name is the system's name in the accessible labels.
  name: string
  ui?: string
  ssh?: string
  rdp?: string
}>()

const { t } = useI18n()
</script>

<template>
  <span class="flex flex-wrap items-center gap-1">
    <slot />
    <a
      v-if="ui"
      :href="ui"
      target="_blank"
      rel="noopener noreferrer"
      class="link-button"
      :title="t('home.links.uiLabel', { name })"
      >{{ t('home.links.ui')
      }}<span class="sr-only">: {{ t('home.links.uiLabel', { name }) }}</span></a
    >
    <a v-if="ssh" :href="ssh" class="link-button" :title="t('home.links.sshLabel', { name })"
      >{{ t('home.links.ssh')
      }}<span class="sr-only">: {{ t('home.links.sshLabel', { name }) }}</span></a
    >
    <button
      v-if="rdp"
      type="button"
      class="link-button"
      :title="t('home.links.rdpLabel', { name })"
      @click="downloadRdp(name, rdp)"
    >
      {{ t('home.links.rdp')
      }}<span class="sr-only">: {{ t('home.links.rdpLabel', { name }) }}</span>
    </button>
  </span>
</template>
