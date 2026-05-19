<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import LyricPlayer from './components/LyricPlayer.vue'
import type { ApiErrorResponse, SongResponse } from './types/song'

const searchTitle = ref('')
const searchArtist = ref('')
const songData = ref<SongResponse | null>(null)
const detailLoading = ref(false)
const detailLoadingMessage = ref('')
const error = ref('')
const detailAbortController = ref<AbortController | null>(null)

const showSearchPanel = computed(() => !songData.value)

const parseJSONResponse = (rawText: string): unknown => {
  if (!rawText.trim()) {
    return null
  }

  try {
    return JSON.parse(rawText)
  } catch {
    return { error: rawText } satisfies ApiErrorResponse
  }
}

const getErrorMessage = (status: number, payload: ApiErrorResponse | null) => {
  switch (status) {
    case 400:
      return payload?.error || '请输入有效的歌名和歌手。'
    case 404:
      return payload?.error || '暂时没有找到这首歌，请换个关键词试试。'
    case 422:
      return payload?.error || '这首歌当前内容较长，请稍后再试。'
    case 429:
      return payload?.error || '当前点歌人数较多，请稍后再试。'
    case 500:
      return payload?.error || '服务暂时未准备好，请稍后重试。'
    case 502:
    case 503:
    case 504:
      return payload?.error || '歌曲发音库暂时繁忙，请稍后重试。'
    default:
      return payload?.error || `请求失败（HTTP ${status}）`
  }
}

const loadSong = async () => {
  const title = searchTitle.value.trim()
  const artist = searchArtist.value.trim()
  if (!title || !artist) {
    error.value = '请输入完整的歌名和歌手后再点歌。'
    return
  }

  detailAbortController.value?.abort()
  const controller = new AbortController()
  detailAbortController.value = controller

  detailLoading.value = true
  detailLoadingMessage.value = '🎵 正在为您全网检索并加载歌曲发音库，请稍候...'
  error.value = ''
  songData.value = null

  try {
    const response = await fetch('http://localhost:8080/api/songs/load', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ title, artist }),
      signal: controller.signal,
    })

    const rawText = await response.text()
    const payload = parseJSONResponse(rawText)
    if (!response.ok) {
      throw new Error(getErrorMessage(response.status, payload as ApiErrorResponse | null))
    }
    if (!payload || typeof payload !== 'object' || !('songId' in payload) || !('lines' in payload)) {
      throw new Error('后端返回的歌曲详情格式不正确。')
    }

    songData.value = payload as SongResponse
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      return
    }
    error.value = err instanceof Error ? err.message : '加载歌曲详情失败，请稍后重试。'
  } finally {
    detailLoading.value = false
    detailLoadingMessage.value = ''
    detailAbortController.value = null
  }
}

const backToSearch = () => {
  songData.value = null
  error.value = ''
}

const handleInputKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Enter') {
    event.preventDefault()
    loadSong()
  }
}

onBeforeUnmount(() => {
  detailAbortController.value?.abort()
})
</script>

<template>
  <main class="min-h-screen bg-stone-50 px-4 py-10 text-stone-800 sm:px-6">
    <div class="mx-auto w-full max-w-4xl">
      <section class="w-full rounded-xl bg-white p-8 shadow-md">
        <div class="space-y-8">
          <div class="space-y-3">
            <p class="text-sm font-semibold uppercase tracking-[0.24em] text-stone-400">LyricalSync</p>
            <h1 class="text-3xl font-bold text-stone-900">KTV 智能点歌练功房</h1>
            <p class="text-sm text-stone-500">
              输入歌名与歌手，一键进入沉浸式发音练功房。
            </p>
          </div>

          <div v-if="showSearchPanel" class="space-y-6">
            <div class="rounded-2xl border border-stone-200 bg-stone-50/80 p-5">
              <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-center">
                <div class="relative">
                  <span class="pointer-events-none absolute inset-y-0 left-4 flex items-center text-stone-400">
                    <svg viewBox="0 0 24 24" class="h-5 w-5 fill-none stroke-current" stroke-width="2">
                      <circle cx="11" cy="11" r="7" />
                      <path d="m20 20-3.5-3.5" />
                    </svg>
                  </span>
                  <input
                    v-model="searchTitle"
                    type="text"
                    placeholder="输入歌名..."
                    class="w-full rounded-full border border-stone-200 bg-white py-3 pl-12 pr-4 text-sm text-stone-700 outline-none transition placeholder:text-stone-400 focus:border-orange-300 focus:ring-4 focus:ring-orange-100"
                    @keydown="handleInputKeydown"
                  />
                </div>

                <input
                  v-model="searchArtist"
                  type="text"
                  placeholder="输入歌手..."
                  class="w-full rounded-full border border-stone-200 bg-white px-4 py-3 text-sm text-stone-700 outline-none transition placeholder:text-stone-400 focus:border-orange-300 focus:ring-4 focus:ring-orange-100"
                  @keydown="handleInputKeydown"
                />

                <button
                  type="button"
                  class="inline-flex items-center justify-center rounded-full bg-orange-500 px-6 py-3 text-sm font-semibold text-white shadow-sm transition hover:bg-orange-600 disabled:cursor-not-allowed disabled:bg-orange-300"
                  :disabled="detailLoading"
                  @click="loadSong"
                >
                  {{ detailLoading ? '点歌中...' : '一键点歌' }}
                </button>
              </div>
            </div>

            <p v-if="error" class="rounded-xl border border-red-100 bg-red-50 px-4 py-3 text-sm text-red-600">
              {{ error }}
            </p>

            <div
              v-if="detailLoading"
              class="rounded-2xl border border-orange-100 bg-orange-50 px-5 py-4 text-sm text-orange-700"
            >
              <p class="font-medium">
                {{ detailLoadingMessage }}
              </p>
            </div>
          </div>

          <div v-else-if="songData" class="space-y-5">
            <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div class="space-y-2">
                <p class="text-sm uppercase tracking-[0.2em] text-stone-400">Practice room</p>
                <h2 class="text-xl font-semibold text-stone-900">
                  {{ songData.title ?? songData.songId }}
                </h2>
                <p v-if="songData.artist" class="text-sm text-stone-500">
                  {{ songData.artist }}
                </p>
              </div>

              <button
                type="button"
                class="inline-flex items-center justify-center rounded-full border border-stone-200 px-4 py-2 text-sm font-medium text-stone-600 transition hover:border-stone-300 hover:text-stone-900"
                @click="backToSearch"
              >
                返回点歌
              </button>
            </div>

            <LyricPlayer :song="songData" />
          </div>
        </div>
      </section>
    </div>
  </main>
</template>
