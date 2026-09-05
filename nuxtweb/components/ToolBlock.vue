<script setup lang="ts">
import { ChevronRight, Loader2, Check, Wrench, AlertTriangle } from 'lucide-vue-next'
import type { ToolBlock as ToolBlockType } from '~/composables/useLicode'

const props = defineProps<{ block: ToolBlockType }>()
const open = ref(false)
</script>

<template>
  <div class="my-2 overflow-hidden rounded-lg border border-zinc-200 bg-zinc-50 text-xs dark:border-zinc-800 dark:bg-zinc-900">
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-zinc-100 dark:hover:bg-zinc-800"
      @click="open = !open"
    >
      <ChevronRight :size="13" class="shrink-0 text-zinc-400 transition-transform" :class="{ 'rotate-90': open }" />
      <Wrench :size="13" class="shrink-0 text-zinc-400" />
      <span class="shrink-0 font-medium">{{ props.block.name }}</span>
      <span v-if="props.block.running" class="flex items-center gap-1 text-zinc-400">
        <Loader2 :size="12" class="animate-spin" /> 执行中…
      </span>
      <span v-else class="flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
        <Check :size="12" /> 完成
      </span>
    </button>
    <div v-if="open" class="border-t border-zinc-200 px-3 py-2 dark:border-zinc-800">
      <div v-if="props.block.args" class="mb-2">
        <div class="mb-1 flex items-center gap-1 text-[10px] font-medium uppercase tracking-wider text-zinc-400">
          <AlertTriangle :size="10" /> 参数
        </div>
        <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-zinc-100 p-2 font-mono text-[11px] leading-relaxed text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">{{ prettyArgs(props.block.args) }}</pre>
      </div>
      <div v-if="props.block.out">
        <div class="mb-1 text-[10px] font-medium uppercase tracking-wider text-zinc-400">输出</div>
        <pre class="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded bg-zinc-100 p-2 font-mono text-[11px] leading-relaxed text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">{{ props.block.out }}</pre>
      </div>
    </div>
  </div>
</template>
