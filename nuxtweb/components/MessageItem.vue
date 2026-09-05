<script setup lang="ts">
import type { ChatMessage } from '~/composables/useLicode'

const props = defineProps<{ msg: ChatMessage }>()

const md = computed(() => (src: string) => renderMarkdown(src))
</script>

<template>
  <div v-if="props.msg.role === 'user'" class="mb-5 flex justify-end">
    <div
      class="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-zinc-900 px-4 py-2.5 text-sm leading-relaxed text-zinc-50 dark:bg-zinc-100 dark:text-zinc-900"
    >
      <div v-if="props.msg.attachments?.length" class="mb-2 flex flex-wrap gap-2">
        <template v-for="att in props.msg.attachments" :key="att.filename">
          <img v-if="att.type === 'image' && att.url" :src="att.url" class="max-h-32 max-w-[200px] rounded-lg object-cover" />
          <span v-else class="flex items-center gap-1 rounded bg-zinc-200 px-2 py-1 text-xs dark:bg-zinc-800">
            📎 {{ att.filename }}
          </span>
        </template>
      </div>
      {{ props.msg.blocks.filter((b) => b.kind === 'text').map((b) => (b as any).text).join('') }}
    </div>
  </div>

  <div v-else class="mb-5 flex gap-3">
    <span
      class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-zinc-200 bg-zinc-50 text-[10px] font-semibold text-zinc-500 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-400"
    >
      AI
    </span>
    <div class="md-body min-w-0 flex-1">
      <template v-for="b in props.msg.blocks" :key="b.id">
        <ToolBlock v-if="b.kind === 'tool'" :block="b" />
        <div v-else class="md-text" v-html="md((b as any).text)" />
      </template>
    </div>
  </div>
</template>
