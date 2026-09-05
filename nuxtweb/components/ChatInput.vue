<script setup lang="ts">
import { SendHorizontal, Square, GitBranch, Eraser, Paperclip, X, Image } from 'lucide-vue-next'
import { Dialog, Button } from 'fuxsto-design'
import { ref, nextTick } from 'vue'
import type { Attachment } from '~/composables/useLicode'

const licode = useLicode()
const { state } = licode
const input = ref('')
const taRef = ref<HTMLTextAreaElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const attachments = ref<Attachment[]>([])
const dragOver = ref(false)

function autoGrow() {
  const el = taRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 192)}`
}

function fileToAttachment(file: File): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const data = (reader.result as string).split(',')[1]
      const isImage = file.type.startsWith('image/')
      resolve({
        type: isImage ? 'image' : 'file',
        mimeType: file.type,
        data,
        filename: file.name,
        url: isImage ? (reader.result as string) : undefined,
      })
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

async function onFileChange(e: Event) {
  const files = (e.target as HTMLInputElement).files
  if (!files) return
  for (const file of Array.from(files)) {
    if (file.size > 10 * 1024 * 1024) {
      Dialog.warning({ title: '文件过大', content: `${file.name} 超过 10MB 限制`, showCancel: false })
      continue
    }
    const att = await fileToAttachment(file)
    attachments.value.push(att)
  }
  ;(e.target as HTMLInputElement).value = ''
}

async function onDrop(e: DragEvent) {
  dragOver.value = false
  if (!e.dataTransfer?.files) return
  for (const file of Array.from(e.dataTransfer.files)) {
    if (file.size > 10 * 1024 * 1024) {
      Dialog.warning({ title: '文件过大', content: `${file.name} 超过 10MB 限制`, showCancel: false })
      continue
    }
    const att = await fileToAttachment(file)
    attachments.value.push(att)
  }
}

function removeAttachment(i: number) {
  attachments.value.splice(i, 1)
}

function doSend() {
  const text = input.value.trim()
  if ((!text && !attachments.value.length) || state.busy) return
  licode.sendMessage(text, attachments.value.length ? attachments.value : undefined)
  input.value = ''
  attachments.value = []
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
        class="rounded-2xl border bg-white p-2 shadow-sm transition-colors focus-within:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-900 dark:focus-within:border-zinc-600"
        :class="dragOver ? 'border-blue-500 bg-blue-50/50 dark:bg-blue-950/30' : 'border-zinc-200'"
        @dragover.prevent="dragOver = true"
        @dragleave.prevent="dragOver = false"
        @drop.prevent="onDrop"
      >
        <div v-if="attachments.length" class="mb-2 flex flex-wrap gap-2 px-2 pt-1">
          <div
            v-for="(att, i) in attachments"
            :key="i"
            class="relative flex items-center gap-2 rounded-lg border border-zinc-200 bg-zinc-50 px-2 py-1 dark:border-zinc-700 dark:bg-zinc-800"
          >
            <img v-if="att.type === 'image' && att.url" :src="att.url" class="h-10 w-10 rounded object-cover" />
            <span v-else class="flex h-10 w-10 items-center justify-center rounded bg-zinc-200 dark:bg-zinc-700">
              <Image :size="16" />
            </span>
            <span class="max-w-[120px] truncate text-xs">{{ att.filename }}</span>
            <button class="text-zinc-400 hover:text-red-500" @click="removeAttachment(i)">
              <X :size="14" />
            </button>
          </div>
        </div>

        <textarea
          ref="taRef"
          v-model="input"
          rows="1"
          class="block w-full resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed outline-none placeholder:text-zinc-400"
          placeholder="输入消息，Enter 发送，Shift+Enter 换行，拖拽或点击📎添加图片/文件…"
          @keydown="onKeydown"
          @input="autoGrow"
        />
        <div class="flex items-center gap-1 px-1 pt-1">
          <input ref="fileInput" type="file" multiple accept="image/*,.pdf,.txt,.md,.json,.csv,.js,.ts,.py,.go,.rs,.java,.c,.cpp,.h,.html,.css,.xml,.yaml,.yml" class="hidden" @change="onFileChange" />
          <Button variant="ghost" size="sm" :icon="Paperclip" title="添加图片/文件" @click="fileInput?.click()" />
          <Button variant="ghost" size="sm" :icon="GitBranch" title="复制会话为分支" @click="doBranch" />
          <Button variant="ghost" size="sm" :icon="Eraser" title="清空当前对话（/clear）" @click="doClear" />
          <span class="flex-1" />
          <Button v-if="state.busy" variant="secondary" size="sm" :icon="Square" @click="licode.interrupt()">
            停止
          </Button>
          <Button v-else variant="primary" size="sm" :icon="SendHorizontal" :disabled="!input.trim() && !attachments.length" @click="doSend">
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
