<script setup lang="ts">
import { WifiOff, RefreshCw } from 'lucide-vue-next'
import { Button } from 'fuxsto-design'

const licode = useLicode()
const { state } = licode
const shownAt = ref<number | null>(null)

watch(
  () => state.wsStatus,
  (v) => {
    if (v === 'disconnected' && shownAt.value === null) shownAt.value = Date.now()
    if (v !== 'disconnected') shownAt.value = null
  },
)

const show = computed(() => {
  if (state.wsStatus !== 'disconnected') return false
  if (shownAt.value === null) return false
  return Date.now() - shownAt.value > 3000
})
</script>

<template>
  <div
    v-if="show"
    class="flex shrink-0 items-center gap-2 border-b border-amber-200 bg-amber-50 px-4 py-1.5 text-xs text-amber-700 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-400"
  >
    <WifiOff :size="13" class="shrink-0" />
    <span class="min-w-0 flex-1">
      无法连接 licode 后端（{{ (useRuntimeConfig().public.licodeBackend as string) || '默认 127.0.0.1:8080' }}），请确认后端已启动，正在自动重连…
    </span>
    <Button size="sm" variant="ghost" :icon="RefreshCw" @click="licode.connect()">重连</Button>
  </div>
</template>
