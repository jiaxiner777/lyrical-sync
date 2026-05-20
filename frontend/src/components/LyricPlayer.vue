<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import type { SongResponse, WordDetail } from '../types/song'

type LineState = 'active' | 'past' | 'future'

const props = defineProps<{
  song: SongResponse
}>()

const currentTime = ref(0)
const isPlaying = ref(false)
const playbackRate = ref<0.75 | 1>(1)
const lineRefs = ref<(HTMLElement | null)[]>([])

let rafId: number | null = null
let playbackStartWallTime = 0
let playbackStartSongTime = 0

const songEndTime = computed(() => props.song.lines.at(-1)?.endTime ?? 0)

const activeLineIndex = computed(() => {
  if (!props.song.lines.length) {
    return -1
  }

  const exactIndex = props.song.lines.findIndex(
    (line) => currentTime.value >= line.startTime && currentTime.value <= line.endTime,
  )
  if (exactIndex >= 0) {
    return exactIndex
  }

  let fallbackIndex = -1
  props.song.lines.forEach((line, index) => {
    if (currentTime.value >= line.startTime) {
      fallbackIndex = index
    }
  })

  return fallbackIndex
})

const cancelPlayback = () => {
  if (rafId !== null) {
    cancelAnimationFrame(rafId)
    rafId = null
  }
}

const pausePlayback = () => {
  cancelPlayback()
  isPlaying.value = false
}

const tick = (now: number) => {
  const elapsedSeconds = ((now - playbackStartWallTime) / 1000) * playbackRate.value
  currentTime.value = Math.min(playbackStartSongTime + elapsedSeconds, songEndTime.value)

  if (currentTime.value >= songEndTime.value) {
    pausePlayback()
    return
  }

  rafId = requestAnimationFrame(tick)
}

const startPlayback = (rate: 0.75 | 1) => {
  if (!props.song.lines.length) {
    return
  }

  if (currentTime.value >= songEndTime.value) {
    currentTime.value = 0
  }

  cancelPlayback()
  playbackRate.value = rate
  isPlaying.value = true
  playbackStartWallTime = performance.now()
  playbackStartSongTime = currentTime.value
  rafId = requestAnimationFrame(tick)
}

const resetPlayback = () => {
  pausePlayback()
  currentTime.value = 0
  playbackRate.value = 1
  lineRefs.value = []
}

const setLineRef = (
  element: Element | ComponentPublicInstance | null,
  index: number,
) => {
  if (element instanceof HTMLElement) {
    lineRefs.value[index] = element
    return
  }

  if (element && '$el' in element && element.$el instanceof HTMLElement) {
    lineRefs.value[index] = element.$el
    return
  }

  lineRefs.value[index] = null
}

const isElision = (detail: WordDetail) => detail.type === 'elision' || detail.opacity !== undefined

const getLineState = (index: number): LineState => {
  if (activeLineIndex.value === index) {
    return 'active'
  }

  if (activeLineIndex.value >= 0 && index < activeLineIndex.value) {
    return 'past'
  }

  return 'future'
}

const isLineActive = (index: number) => getLineState(index) === 'active'

const getLineClass = (index: number) => {
  const state = getLineState(index)

  if (state === 'active') {
    return 'scale-[1.02] border-orange-100 bg-orange-50/80 opacity-100 shadow-sm ring-1 ring-orange-100'
  }

  if (state === 'past') {
    return 'border-transparent bg-white/50 opacity-45'
  }

  return 'border-transparent bg-transparent opacity-25'
}

const getWordClass = (state: LineState) => {
  if (state === 'active') {
    return 'text-2xl font-bold text-stone-800'
  }

  if (state === 'past') {
    return 'text-xl font-semibold text-stone-400'
  }

  return 'text-xl font-semibold text-stone-300'
}

const getWordRowClass = (detail: WordDetail) => {
  if (detail.linkWithNext) {
    return 'relative inline-flex items-start pr-4'
  }

  return 'inline-flex items-start'
}

const getPinyinClass = (_detail: WordDetail, state: LineState) => {
  if (state === 'active') {
    return 'text-lg font-medium text-orange-500'
  }

  if (state === 'past') {
    return 'text-base font-medium text-stone-400'
  }

  return 'text-base font-medium text-stone-300'
}

watch(
  () => props.song,
  async () => {
    resetPlayback()
    await nextTick()
  },
  { immediate: true },
)

watch(activeLineIndex, async (newIndex, oldIndex) => {
  if (newIndex < 0 || newIndex === oldIndex) {
    return
  }

  await nextTick()
  lineRefs.value[newIndex]?.scrollIntoView({
    behavior: 'smooth',
    block: 'center',
  })
})

onBeforeUnmount(() => {
  cancelPlayback()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
      <div class="space-y-2">
        <p class="text-sm font-semibold uppercase tracking-[0.28em] text-stone-400">Practice room</p>
        <h2 class="text-3xl font-semibold tracking-tight text-stone-900">
          {{ song.title ?? song.songId }}
        </h2>
        <p v-if="song.artist" class="text-sm text-stone-500">{{ song.artist }}</p>
      </div>

      <div class="rounded-3xl border border-stone-200 bg-white px-5 py-4 shadow-sm">
        <p class="text-xs font-semibold uppercase tracking-[0.24em] text-stone-400">Live timing</p>
        <p class="mt-2 text-lg font-semibold text-stone-800">
          {{ currentTime.toFixed(2) }}s / {{ songEndTime.toFixed(2) }}s
        </p>
        <p class="mt-1 text-sm text-stone-500">
          {{ isPlaying ? `当前以 ${playbackRate.toFixed(2)}x 模拟播放中` : '当前暂停，可随时继续练习' }}
        </p>
      </div>
    </div>

    <div class="rounded-[2rem] border border-stone-100 bg-white/85 p-4 shadow-[0_24px_80px_rgba(0,0,0,0.06)] backdrop-blur sm:p-5">
      <div class="max-h-[66vh] overflow-y-auto pr-2 scroll-smooth">
        <div class="space-y-5 py-3 sm:space-y-6 sm:py-5">
          <article
            v-for="(line, lineIndex) in song.lines"
            :key="`${song.songId}-${lineIndex}`"
            :ref="(el) => setLineRef(el, lineIndex)"
            class="rounded-3xl border px-4 py-5 transition-all duration-300 ease-out sm:px-5"
            :class="getLineClass(lineIndex)"
          >
            <div class="mb-3 flex items-center justify-between gap-3 text-xs text-stone-400">
              <span>
                {{ line.startTime.toFixed(2) }}s - {{ line.endTime.toFixed(2) }}s
              </span>
              <span
                v-if="isLineActive(lineIndex)"
                class="font-semibold uppercase tracking-[0.18em] text-orange-400"
              >
                Active line
              </span>
            </div>

            <div class="flex flex-wrap gap-x-3 gap-y-4">
              <div
                v-for="(detail, detailIndex) in line.details"
                :key="`${detail.word}-${detailIndex}`"
                class="flex min-w-fit flex-col items-center"
              >
                <span :class="[getWordRowClass(detail), isElision(detail) ? 'opacity-40' : '']">
                  <span :class="getWordClass(getLineState(lineIndex))">
                    {{ isElision(detail) ? `(${detail.word})` : detail.word }}
                  </span>
                  <span
                    v-if="detail.linkWithNext"
                    class="absolute -right-0.5 top-0 text-base leading-none text-emerald-400"
                  >
                    ︶
                  </span>
                </span>

                <span class="mt-1 leading-none">
                  <span :class="getPinyinClass(detail, getLineState(lineIndex))">
                    {{ detail.pinyin }}
                  </span>
                </span>
              </div>
            </div>
          </article>
        </div>
      </div>
    </div>

    <div class="rounded-[1.75rem] border border-stone-200 bg-white px-5 py-4 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div class="space-y-1">
          <p class="text-xs font-semibold uppercase tracking-[0.24em] text-stone-400">无损倍速控制台</p>
          <p class="text-sm text-stone-500">保持当前句位，原速、慢速、暂停可随时切换。</p>
        </div>

        <div class="flex flex-wrap gap-3">
          <button
            type="button"
            class="inline-flex items-center justify-center rounded-full bg-orange-500 px-5 py-2.5 text-sm font-semibold text-white transition hover:bg-orange-600"
            @click="startPlayback(1)"
          >
            原速播放 1.0x
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center rounded-full bg-orange-100 px-5 py-2.5 text-sm font-semibold text-orange-700 transition hover:bg-orange-200"
            @click="startPlayback(0.75)"
          >
            慢速跟练 0.75x
          </button>
          <button
            type="button"
            class="inline-flex items-center justify-center rounded-full border border-stone-200 px-5 py-2.5 text-sm font-semibold text-stone-600 transition hover:border-stone-300 hover:text-stone-900"
            @click="pausePlayback"
          >
            暂停
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
