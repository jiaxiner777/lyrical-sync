export interface WordDetail {
  word: string
  pinyin: string
  type: string
  opacity?: number
  linkWithNext?: boolean
}

export interface SongLine {
  startTime: number
  endTime: number
  originalText: string
  details: WordDetail[]
}

export interface SongResponse {
  songId: string
  title?: string
  artist?: string
  lines: SongLine[]
}

export interface SongSearchResult {
  id: number
  title: string
  artist: string
}

export interface ApiErrorResponse {
  error: string
  code?: string
  retryable?: boolean
}
