<script setup lang="ts">
import { SendHorizontal, Square, GitBranch, Eraser, Paperclip, X, Image, Plus } from 'lucide-vue-next'
import { Dialog, Button } from 'fuxsto-design'
import { ref, nextTick, computed } from 'vue'
import type { Attachment } from '~/composables/useLicode'

const licode = useLicode()
const { state } = licode
const input = ref('')
const taRef = ref<HTMLTextAreaElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const attachments = ref<Attachment[]>([])
const dragOver = ref(false)
const showSlashMenu = ref(false)
const slashMenuIndex = ref(0)

const slashCommands = [
  { key: '/clear', label: '清空对话', desc: '清空当前会话的全部消息', icon: Eraser, action: doClear },
  { key: '/branch', label: '复制分支', desc: '复制当前对话为新会话', icon: GitBranch, action: doBranch },
  { key: '/new', label: '新建对话', desc: '创建一个新会话', icon: Plus, action: () => licode.newSession() },
  { key: '/attach', label: '添加附件', desc: '上传图片或文件', icon: Paperclip, action: () => fileInput?.value?.click() },
  { key: '/interrupt', label: '停止生成', desc: '中断当前正在进行的回复', icon: Square, action: () => licode.interrupt() },
]

const filteredCommands = computed(() => {
  const text = input.value.trim().toLowerCase()
  if (!text.startsWith('/')) return []
  const query = text.slice(1)
  return slashCommands.filter((c) => c.key.includes(query) || c.label.includes(query))
})

function onInput() {
  autoGrow()
  const text = input.value.trim()
  showSlashMenu.value = text.startsWith('/') && filteredCommands.value.length > 0
  slashMenuIndex.value = 0
}

function onKeydown(e: KeyboardEvent) {
  if (showSlashMenu.value) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      slashMenuIndex.value = Math.min(slashMenuIndex.value + 1, filteredCommands.value.length - 1)
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      slashMenuIndex.value = Math.max(slashMenuIndex.value - 1, 0)
      return
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      const cmd = filteredCommands.value[slashMenuIndex.value]
      if (cmd) {
        cmd.action()
        input.value = ''
        showSlashMenu.value = false
      }
      return
    }
    if (e.key === 'Escape') {
      showSlashMenu.value = false
      return
    }
  }
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    doSend()
  }
}

function selectCommand(cmd: typeof slashCommands[0]) {
  cmd.action()
  input.value = ''
  showSlashMenu.value = false
}

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
      <div class="relative mx-auto w-full max-w-3xl">
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
          placeholder="输入消息，Enter 发送，Shift+Enter 换行，/ 快速命令…"
          @keydown="onKeydown"
          @input="onInput"
        />

        <div
          v-if="showSlashMenu && filteredCommands.length"
          class="absolute left-2 right-2 bottom-full mb-1 z-50 max-h-60 overflow-y-auto rounded-xl border border-zinc-200 bg-white shadow-lg dark:border-zinc-700 dark:bg-zinc-900"
        >
          <div
            v-for="(cmd, i) in filteredCommands"
            :key="cmd.key"
            class="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm transition-colors"
            :class="i === slashMenuIndex ? 'bg-zinc-100 dark:bg-zinc-800' : 'hover:bg-zinc-50 dark:hover:bg-zinc-800/60'"
            @click="selectCommand(cmd)"
          >
            <component :is="cmd.icon" :size="14" class="shrink-0 text-zinc-400" />
            <span class="font-medium">{{ cmd.label }}</span>
            <span class="flex-1 truncate text-xs text-zinc-400">{{ cmd.desc }}</span>
            <kbd class="rounded bg-zinc-100 px-1.5 py-0.5 text-[10px] text-zinc-500 dark:bg-zinc-800">{{ cmd.key }}</kbd>
          </div>
        </div>
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
