<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts" generic="Row extends object">
import { useI18n } from 'vue-i18n'

export interface Column {
  key: string
  label: string
  class?: string
}

const props = withDefaults(
  defineProps<{
    columns: Column[]
    rows: Row[]
    rowKey: (row: Row) => string | number
    caption?: string
    loading?: boolean
    empty?: string
  }>(),
  { caption: '', loading: false, empty: '' },
)

const { t } = useI18n()

function value(row: Row, key: string): string {
  const v = (row as Record<string, unknown>)[key]
  return v === undefined || v === null ? '' : String(v)
}
</script>

<template>
  <div class="overflow-x-auto rounded-md border border-border">
    <table class="w-full text-left text-sm">
      <caption v-if="props.caption" class="sr-only">
        {{
          props.caption
        }}
      </caption>
      <thead class="bg-surface-sunken text-fg-muted">
        <tr>
          <th
            v-for="c in props.columns"
            :key="c.key"
            scope="col"
            class="px-3 py-2 font-medium"
            :class="c.class"
          >
            {{ c.label }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="props.loading">
          <td :colspan="props.columns.length" class="px-3 py-6 text-center text-fg-muted">
            {{ t('common.loading') }}
          </td>
        </tr>
        <tr v-else-if="props.rows.length === 0">
          <td :colspan="props.columns.length" class="px-3 py-6 text-center text-fg-muted">
            {{ props.empty || t('common.empty') }}
          </td>
        </tr>
        <tr
          v-for="row in props.loading ? [] : props.rows"
          :key="props.rowKey(row)"
          class="border-t border-border even:bg-surface-raised"
        >
          <td v-for="c in props.columns" :key="c.key" class="px-3 py-2 align-top" :class="c.class">
            <slot :name="`cell-${c.key}`" :row="row">{{ value(row, c.key) }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
