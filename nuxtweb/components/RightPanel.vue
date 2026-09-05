<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { Button, Tabs } from 'fuxsto-design'

const { state } = useLicode()

const tabs = [
  { label: '信息', value: 'info' },
  { label: '文件', value: 'files' },
  { label: '搜索', value: 'search' },
  { label: '审计', value: 'audit' },
]
</script>

<template>
  <aside
    class="flex h-full shrink-0 flex-col border-l border-zinc-200 bg-white transition-all duration-200 dark:border-zinc-800 dark:bg-zinc-900"
    :class="state.rightTab ? 'w-80' : 'w-0 overflow-hidden border-l-0'"
  >
    <template v-if="state.rightTab">
      <div class="flex shrink-0 items-center gap-2 border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
        <Tabs
          :model-value="state.rightTab"
          :options="tabs"
          size="sm"
          variant="pill"
          class="flex-1"
          @update:model-value="state.rightTab = $event as any"
        />
        <Button variant="ghost" size="sm" :icon="X" @click="state.rightTab = ''" />
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto">
        <InfoPanel v-if="state.rightTab === 'info'" />
        <FilesPanel v-else-if="state.rightTab === 'files'" />
        <SearchPanel v-else-if="state.rightTab === 'search'" />
        <AuditPanel v-else />
      </div>
    </template>
  </aside>
</template>
