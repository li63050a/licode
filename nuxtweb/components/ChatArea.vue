<script setup lang="ts">
import { Bot, Loader2 } from 'lucide-vue-next'
import { Chip, Empty, Message } from 'fuxsto-design'
const licode = useLicode()
const { state } = licode
const listRef = ref<HTMLElement | null>(null)
const pinned = ref(true)

const suggestions = [
  '浏览并解释这个项目的结构',
  '帮我写一个单元测试',
  '找出代码里的潜在 bug',
  '总结当前工作目录的改动',
]

function onScroll() {
  const el = listRef.value
  if (!el) return
  pinned.value = el.scrollTop + el.clientHeight >= el.scrollHeight - 80
}

function scrollToBottom() {
  nextTick(() => {
    const el = listRef.value
    if (el && pinned.value) el.scrollTop = el.scrollHeight
  })
}

watch(() => [state.messages, state.statusText], scrollToBottom, { deep: true })

function onRootClick(e: MouseEvent) {
  const t = (e.target as HTMLElement).closest('.md-copy') as HTMLElement | null
  if (t?.dataset.code) {
    navigator.clipboard
      .writeText(decodeURIComponent(t.dataset.code))
      .then(() => Message.success('代码已复制'))
      .catch(() => Message.error('复制失败'))
  }
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div ref="listRef" class="min-h-0 flex-1 overflow-y-auto" @scroll="onScroll" @click="onRootClick">
      <div class="mx-auto w-full max-w-3xl px-4 py-6">
        <div v-if="!state.messages.length" class="flex flex-col items-center pt-24">
          <span
            class="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900"
          >
            <Bot :size="26" />
          </span>
          <h1 class="text-xl font-semibold tracking-tight">licode</h1>
          <p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
            本地 AI 编程助手 · 纯 Go · WASM 插件 · 子代理编排
          </p>
          <div class="mt-6 flex flex-wrap justify-center gap-2">
            <Chip v-for="s in suggestions" :key="s" variant="outline" size="sm" @click="licode.sendMessage(s)">
              {{ s }}
            </Chip>
          </div>
        </div>

        <MessageItem v-for="m in state.messages" :key="m.id" :msg="m" />

        <div v-if="state.busy && state.statusText" class="mt-2 flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
          <Loader2 :size="13" class="animate-spin" />
          {{ state.statusText }}
        </div>
      </div>
    </div>

    <AskBar v-if="state.ask" />
    <ChatInput />
  </div>
</template>
