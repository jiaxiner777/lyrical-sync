<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import type { SongResponse, WordDetail } from '../types/song'

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

const isLineActive = (index: number) => activeLineIndex.value === index

const getLineClass = (index: number) => {
  if (isLineActive(index)) {
    return 'scale-[1.02] opacity-100'
  }

  return 'scale-100 opacity-55'
}

const getWordClass = (detail: WordDetail, isActive: boolean) => {
  if (!isActive) {
    return 'text-xl font-bold text-gray-300'
  }

  if (isElision(detail)) {
    return 'text-2xl font-bold text-gray-500'
  }

  return 'text-2xl font-bold text-gray-800'
}

const getPinyinClass = (detail: WordDetail, isActive: boolean) => {
  if (!isActive) {
    return 'text-base font-medium text-gray-300'
  }

  if (isElision(detail)) {
    return 'text-lg font-medium text-gray-400'
  }

  return 'text-lg font-medium text-orange-500'
}

const getTokenStyle = (detail: WordDetail, isActive: boolean) => {
  if (!isActive) {
    return {
      opacity: isElision(detail) ? 0.3 : 0.45,
    }
  }

  if (isElision(detail)) {
    return {
      opacity: detail.opacity ?? 0.4,
    }
  }

  return undefined
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
    <div class="flex flex-col gap-4 border-b border-stone-100 pb-6 sm:flex-row sm:items-end sm:justify-between">
      <div class="space-y-2">
        <p class="text-sm font-semibold uppercase tracking-[0.24em] text-stone-400">Now practicing</p>
        <h2 class="text-2xl font-bold text-stone-900">{{ song.title ?? song.songId }}</h2>
        <p v-if="song.artist" class="text-sm text-stone-500">{{ song.artist }}</p>
        <p class="text-sm text-stone-500">
          当前时间 {{ currentTime.toFixed(2) }}s / {{ songEndTime.toFixed(2) }}s
        </p>
      </div>

      <div class="flex flex-wrap gap-3">
        <button
          type="button"
          class="inline-flex items-center justify-center rounded-full bg-orange-500 px-5 py-2.5 text-sm font-semibold text-white transition hover:bg-orange-600"
          @click="startPlayback(1)"
        >
          开始模拟播放 (1.0x 原速)
        </button>
        <button
          type="button"
          class="inline-flex items-center justify-center rounded-full bg-orange-100 px-5 py-2.5 text-sm font-semibold text-orange-700 transition hover:bg-orange-200"
          @click="startPlayback(0.75)"
        >
          慢速跟练 (0.75x)
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

    <div class="max-h-[68vh] overflow-y-auto pr-2">
      <div class="space-y-6 py-8">
        <article
          v-for="(line, lineIndex) in song.lines"
          :key="`${song.songId}-${lineIndex}`"
          :ref="(el) => setLineRef(el, lineIndex)"
          class="rounded-2xl px-4 py-5 transition-all duration-300 ease-out"
          :class="[
            getLineClass(lineIndex),
            isLineActive(lineIndex)
              ? 'bg-orange-50/70 shadow-sm ring-1 ring-orange-100'
              : 'bg-transparent',
          ]"
        >
          <div class="mb-3 flex items-center justify-between gap-3 text-xs text-stone-400">
            <span>
              {{ line.startTime.toFixed(2) }}s - {{ line.endTime.toFixed(2) }}s
            </span>
            <span v-if="isLineActive(lineIndex)" class="font-semibold uppercase tracking-[0.18em] text-orange-400">
              Active line
            </span>
          </div>

          <div class="flex flex-wrap gap-x-3 gap-y-4">
            <div
              v-for="(detail, detailIndex) in line.details"
              :key="`${detail.word}-${detailIndex}`"
              class="flex min-w-fit flex-col items-center"
              :style="getTokenStyle(detail, isLineActive(lineIndex))"
            >
              <span :class="getWordClass(detail, isLineActive(lineIndex))">
                {{ detail.word }}
              </span>

              <span class="mt-1 flex items-center gap-1 leading-none">
                <span :class="getPinyinClass(detail, isLineActive(lineIndex))">
                  {{ detail.pinyin }}
                </span>
                <span
                  v-if="detail.linkWithNext"
                  class="text-base leading-none"
                  :class="isLineActive(lineIndex) ? 'text-emerald-500' : 'text-gray-300'"
                >
                  ︶
                </span>
              </span>
            </div>
          </div>
        </article>
      </div>
    </div>
  </div>
</template>
