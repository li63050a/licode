<script setup lang="ts">
const props = defineProps<{ diff: string }>()

type Line = { cls: string; text: string }

const lines = computed<Line[]>(() =>
  props.diff.split('\n').map((l) => {
    if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('diff ') || l.startsWith('index '))
      return { cls: 'text-zinc-400', text: l }
    if (l.startsWith('@@')) return { cls: 'text-sky-600 dark:text-sky-400', text: l }
    if (l.startsWith('+')) return { cls: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400', text: l }
    if (l.startsWith('-')) return { cls: 'bg-red-500/10 text-red-600 dark:text-red-400', text: l }
    return { cls: 'text-zinc-500 dark:text-zinc-400', text: l }
  }),
)
</script>

<template>
  <pre class="overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300"><code><span
    v-for="(l, i) in lines"
    :key="i"
    class="block"
    :class="l.cls"
>{{ l.text }}</span></code></pre>
</template>
