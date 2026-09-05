<script setup lang="ts">
import { ShieldAlert, ChevronRight } from 'lucide-vue-next'
import { Button } from 'fuxsto-design'

const licode = useLicode()
const { state } = licode
const showArgs = ref(false)
</script>

<template>
  <div class="mx-auto w-full max-w-3xl px-4 pb-2">
    <div
      class="rounded-xl border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-500/30 dark:bg-amber-500/10"
    >
      <div class="flex flex-wrap items-center gap-2">
        <ShieldAlert :size="16" class="shrink-0 text-amber-600 dark:text-amber-400" />
        <span class="font-medium">
          工具 <code class="rounded bg-amber-100 px-1 font-mono text-xs dark:bg-amber-500/20">{{ state.ask?.toolName }}</code> 请求执行
        </span>
        <button
          class="flex items-center text-xs text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200"
          @click="showArgs = !showArgs"
        >
          <ChevronRight :size="12" class="transition-transform" :class="{ 'rotate-90': showArgs }" />
          参数
        </button>
        <span class="flex-1" />
        <div class="flex items-center gap-2">
          <Button size="sm" variant="outline" danger @click="licode.replyAsk(false, false)">拒绝</Button>
          <Button size="sm" variant="outline" @click="licode.replyAsk(true, true)">始终允许</Button>
          <Button size="sm" variant="primary" @click="licode.replyAsk(true, false)">允许</Button>
        </div>
      </div>
      <pre
        v-if="showArgs && state.ask?.toolArgs"
        class="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-white/60 p-2 font-mono text-[11px] text-zinc-600 dark:bg-black/20 dark:text-zinc-300"
      >{{ prettyArgs(state.ask.toolArgs) }}</pre>
    </div>
  </div>
</template>
