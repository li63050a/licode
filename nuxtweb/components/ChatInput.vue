<script setup lang="ts">
import { SendHorizontal, Square, GitBranch, Eraser } from 'lucide-vue-next'
import { Dialog, Button } from 'fuxsto-design'

const licode = useLicode()
const { state } = licode
const input = ref('')
const taRef = ref<HTMLTextAreaElement | null>(null)

function autoGrow() {
  const el = taRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 192)}px`
}

function doSend() {
  const text = input.value.trim()
  if (!text || state.busy) return
  licode.sendMessage(text)
  input.value = ''
  nextTick(autoGrow)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    doSend()
  }
}

function doBranch() {
  if (!state.sessionId) {
    Dialog.warning({ title: '无法分支', content: '当前没有活跃会话', showCancel: false })
    return
  }
  Dialog.confirm({
    title: '创建分支',
    content: '复制当前完整对话为一个新会话？',
    confirmText: '创建',
    onConfirm: () => licode.branchSession(),
  })
}

function doClear() {
  Dialog.confirm({
    title: '清空对话',
    content: '发送 /clear 将清空当前会话的全部消息，确定？',
    danger: true,
    confirmText: '清空',
    onConfirm: () => licode.sendMessage('/clear'),
  })
}
</script>

<template>
  <div class="shrink-0 px-4 pb-4">
    <div class="mx-auto w-full max-w-3xl">
      <div
        class="rounded-2xl border border-zinc-200 bg-white p-2 shadow-sm transition-colors focus-within:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-900 dark:focus-within:border-zinc-600"
      >
        <textarea
          ref="taRef"
          v-model="input"
          rows="1"
          class="block w-full resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed outline-none placeholder:text-zinc-400"
          placeholder="输入消息，Enter 发送，Shift+Enter 换行…"
          @keydown="onKeydown"
          @input="autoGrow"
        />
        <div class="flex items-center gap-1 px-1 pt-1">
          <Button variant="ghost" size="sm" :icon="GitBranch" title="复制会话为分支" @click="doBranch" />
          <Button variant="ghost" size="sm" :icon="Eraser" title="清空当前对话（/clear）" @click="doClear" />
          <span class="flex-1" />
          <Button v-if="state.busy" variant="secondary" size="sm" :icon="Square" @click="licode.interrupt()">
            停止
          </Button>
          <Button v-else variant="primary" size="sm" :icon="SendHorizontal" :disabled="!input.trim()" @click="doSend">
            发送
          </Button>
        </div>
      </div>
      <p class="mt-1.5 text-center text-[11px] text-zinc-400">
        licode 可能会犯错，请检查重要信息。
      </p>
    </div>
  </div>
</template>
