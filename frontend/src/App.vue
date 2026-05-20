<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import LyricPlayer from './components/LyricPlayer.vue'
import type { ApiErrorResponse, SongResponse, SongSearchResult } from './types/song'

type Page = 'home' | 'player'

const songApiBase = 'http://localhost:8080/api/songs'

const currentPage = ref<Page>('home')
const searchTitle = ref('')
const searchArtist = ref('')
const songData = ref<SongResponse | null>(null)
const detailLoading = ref(false)
const detailLoadingMessage = ref('')
const error = ref('')
const recommendedSongs = ref<SongSearchResult[]>([])
const recommendationLoading = ref(false)
const recommendationError = ref('')
const detailAbortController = ref<AbortController | null>(null)

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

const isSongResponse = (payload: unknown): payload is SongResponse => {
  if (!payload || typeof payload !== 'object') {
    return false
  }

  const record = payload as Record<string, unknown>
  return typeof record.songId === 'string' && Array.isArray(record.lines)
}

const isSongSearchResult = (payload: unknown): payload is SongSearchResult => {
  if (!payload || typeof payload !== 'object') {
    return false
  }

  const record = payload as Record<string, unknown>
  return (
    typeof record.id === 'number' &&
    typeof record.title === 'string' &&
    typeof record.artist === 'string'
  )
}

const requestSongDetail = async (
  input: RequestInfo | URL,
  init: RequestInit,
  loadingMessage: string,
) => {
  detailAbortController.value?.abort()
  const controller = new AbortController()
  detailAbortController.value = controller

  detailLoading.value = true
  detailLoadingMessage.value = loadingMessage
  error.value = ''

  try {
    const response = await fetch(input, {
      ...init,
      signal: controller.signal,
    })

    const rawText = await response.text()
    const payload = parseJSONResponse(rawText)
    if (!response.ok) {
      throw new Error(getErrorMessage(response.status, payload as ApiErrorResponse | null))
    }
    if (!isSongResponse(payload)) {
      throw new Error('后端返回的歌曲详情格式不正确。')
    }

    songData.value = payload
    currentPage.value = 'player'
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

const loadRecommendations = async () => {
  recommendationLoading.value = true
  recommendationError.value = ''

  try {
    const response = await fetch(`${songApiBase}/search`)
    const rawText = await response.text()
    const payload = parseJSONResponse(rawText)

    if (!response.ok) {
      const apiError = payload as ApiErrorResponse | null
      throw new Error(apiError?.error || '推荐金曲加载失败，请稍后再试。')
    }

    if (!Array.isArray(payload)) {
      throw new Error('推荐金曲数据格式不正确。')
    }

    recommendedSongs.value = payload.filter(isSongSearchResult)
  } catch (err) {
    recommendationError.value = err instanceof Error ? err.message : '推荐金曲加载失败，请稍后重试。'
    recommendedSongs.value = []
  } finally {
    recommendationLoading.value = false
  }
}

const loadSong = async () => {
  if (detailLoading.value) {
    return
  }

  const title = searchTitle.value.trim()
  const artist = searchArtist.value.trim()
  if (!title || !artist) {
    error.value = '请输入完整的歌名和歌手后再点歌。'
    return
  }

  await requestSongDetail(
    `${songApiBase}/load`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ title, artist }),
    },
    '🎵 正在为您全网检索并加载歌曲发音库...',
  )
}

const openRecommendedSong = async (song: SongSearchResult) => {
  if (detailLoading.value) {
    return
  }

  await requestSongDetail(
    `${songApiBase}/${song.id}`,
    {
      method: 'GET',
    },
    `🎵 正在唤起《${song.title}》的缓存发音库...`,
  )
}

const backToSearch = () => {
  detailAbortController.value?.abort()
  songData.value = null
  currentPage.value = 'home'
  error.value = ''
}

const handleInputKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Enter') {
    event.preventDefault()
    void loadSong()
  }
}

onMounted(() => {
  void loadRecommendations()
})

onBeforeUnmount(() => {
  detailAbortController.value?.abort()
})
</script>

<template>
  <main class="min-h-screen bg-stone-50 text-stone-800">
    <Transition name="page-fade" mode="out-in">
      <section
        v-if="currentPage === 'home'"
        key="home"
        class="flex min-h-screen items-center px-4 py-12 sm:px-6"
      >
        <div class="mx-auto w-full max-w-6xl space-y-10">
          <div class="space-y-4 text-center">
            <p class="text-sm font-semibold uppercase tracking-[0.32em] text-stone-400">LyricalSync</p>
            <h1 class="text-4xl font-semibold tracking-tight text-stone-900 sm:text-5xl">
              KTV 智能点歌大厅
            </h1>
            <p class="mx-auto max-w-2xl text-sm text-stone-500 sm:text-base">
              输入歌名与歌手，一键切入沉浸式练功房；或者直接点开缓存金曲，即刻开练。
            </p>
          </div>

          <div class="mx-auto w-full max-w-2xl space-y-4">
            <div class="rounded-full border border-stone-200 bg-white px-4 py-4 shadow-sm transition focus-within:shadow-md sm:px-5">
              <div class="flex flex-col gap-3 sm:flex-row sm:items-center">
                <label class="sr-only" for="song-title">歌名</label>
                <input
                  id="song-title"
                  v-model="searchTitle"
                  type="text"
                  placeholder="歌名"
                  class="min-w-0 flex-1 rounded-full bg-stone-50 px-5 py-3 text-sm text-stone-700 outline-none transition placeholder:text-stone-400 focus:bg-white focus:ring-2 focus:ring-orange-100"
                  @keydown="handleInputKeydown"
                />

                <label class="sr-only" for="song-artist">歌手</label>
                <input
                  id="song-artist"
                  v-model="searchArtist"
                  type="text"
                  placeholder="歌手"
                  class="min-w-0 flex-1 rounded-full bg-stone-50 px-5 py-3 text-sm text-stone-700 outline-none transition placeholder:text-stone-400 focus:bg-white focus:ring-2 focus:ring-orange-100"
                  @keydown="handleInputKeydown"
                />

                <button
                  type="button"
                  class="inline-flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-orange-500 text-white shadow-sm transition hover:bg-orange-600 disabled:cursor-not-allowed disabled:bg-orange-300"
                  :disabled="detailLoading"
                  @click="loadSong"
                >
                  <svg
                    viewBox="0 0 24 24"
                    class="h-5 w-5 fill-none stroke-current"
                    stroke-width="2"
                  >
                    <circle cx="11" cy="11" r="7" />
                    <path d="m20 20-3.5-3.5" />
                  </svg>
                  <span class="sr-only">智能点歌</span>
                </button>
              </div>
            </div>

            <div
              v-if="detailLoading"
              class="rounded-3xl border border-orange-100 bg-white px-5 py-4 shadow-sm"
            >
              <div class="flex items-center justify-between gap-4 text-sm text-orange-700">
                <p class="font-medium">{{ detailLoadingMessage }}</p>
                <span class="text-xs uppercase tracking-[0.24em] text-orange-400">Loading</span>
              </div>
              <div class="mt-3 h-2 overflow-hidden rounded-full bg-orange-100">
                <div class="loading-bar h-full rounded-full bg-orange-400" />
              </div>
            </div>

            <p
              v-if="error"
              class="rounded-2xl border border-red-100 bg-red-50 px-4 py-3 text-sm text-red-600 shadow-sm"
            >
              {{ error }}
            </p>
          </div>

          <div class="space-y-5">
            <div class="flex flex-col gap-2 text-center sm:text-left">
              <p class="text-sm font-semibold uppercase tracking-[0.28em] text-stone-400">
                Quick start
              </p>
              <div class="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
                <h2 class="text-2xl font-semibold text-stone-900">推荐金曲</h2>
                <p class="text-sm text-stone-500">点击缓存命中的经典歌曲，直接秒切沉浸练功房。</p>
              </div>
            </div>

            <div v-if="recommendationLoading" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <div
                v-for="index in 6"
                :key="index"
                class="h-28 animate-pulse rounded-xl border border-stone-100 bg-white shadow-sm"
              />
            </div>

            <p
              v-else-if="recommendationError"
              class="rounded-2xl border border-stone-200 bg-white px-4 py-3 text-sm text-stone-500 shadow-sm"
            >
              {{ recommendationError }}
            </p>

            <p
              v-else-if="recommendedSongs.length === 0"
              class="rounded-2xl border border-stone-200 bg-white px-4 py-3 text-sm text-stone-500 shadow-sm"
            >
              暂无缓存金曲，先从上方输入歌名与歌手试试看。
            </p>

            <div v-else class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <button
                v-for="song in recommendedSongs"
                :key="song.id"
                type="button"
                class="group rounded-xl border border-stone-100 bg-white p-4 text-left shadow-sm transition hover:-translate-y-1 hover:shadow-md disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="detailLoading"
                @click="openRecommendedSong(song)"
              >
                <div class="flex h-full flex-col justify-between gap-5">
                  <div class="space-y-2">
                    <p class="text-xs font-semibold uppercase tracking-[0.24em] text-orange-400">缓存命中</p>
                    <h3 class="text-lg font-semibold text-stone-900 transition group-hover:text-orange-500">
                      {{ song.title }}
                    </h3>
                    <p class="text-sm text-stone-500">{{ song.artist }}</p>
                  </div>

                  <div class="flex items-center justify-between text-sm text-stone-400">
                    <span>立即开练</span>
                    <span class="text-orange-400 transition group-hover:translate-x-1">→</span>
                  </div>
                </div>
              </button>
            </div>
          </div>
        </div>
      </section>

      <section v-else key="player" class="min-h-screen px-4 py-6 sm:px-6">
        <div class="mx-auto flex w-full max-w-6xl flex-col gap-6">
          <button
            type="button"
            class="inline-flex items-center gap-2 self-start text-sm font-medium text-stone-400 transition hover:text-orange-500"
            @click="backToSearch"
          >
            <span>←</span>
            <span>返回点歌大厅</span>
          </button>

          <LyricPlayer v-if="songData" :song="songData" />
        </div>
      </section>
    </Transition>
  </main>
</template>

<style scoped>
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity 0.24s ease, transform 0.24s ease;
}

.page-fade-enter-from,
.page-fade-leave-to {
  opacity: 0;
  transform: translateY(14px);
}

.loading-bar {
  width: 36%;
  animation: loading-slide 1.1s ease-in-out infinite;
}

@keyframes loading-slide {
  0% {
    transform: translateX(-20%);
  }

  50% {
    transform: translateX(130%);
  }

  100% {
    transform: translateX(-20%);
  }
}
</style>
