package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type neteaseSearchResponse struct {
	Result struct {
		Songs []struct {
			ID int64 `json:"id"`
		} `json:"songs"`
	} `json:"result"`
}

type neteaseLyricResponse struct {
	LRC struct {
		Lyric string `json:"lyric"`
	} `json:"lrc"`
}

var lyricTimestampPattern = regexp.MustCompile(`\[\d{2}:\d{2}(?:\.\d{1,3})?\]`)
var containsHanPattern = regexp.MustCompile(`\p{Han}`)
var containsLatinPattern = regexp.MustCompile(`[A-Za-z]`)

func FetchLyrics(ctx context.Context, title string, artist string) (string, error) {
	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)
	if title == "" || artist == "" {
		return "", newServiceError(ServiceErrorInvalidInput, "title and artist are required to fetch lyrics", false, nil)
	}
	if err := ctx.Err(); err != nil {
		return "", newServiceError(ServiceErrorUpstreamTimeout, "lyrics fetch request was cancelled", true, err)
	}

	lyrics, err := fetchExternalLyricsFromInternet(title, artist)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", newServiceError(ServiceErrorUpstreamTimeout, "lyrics fetch request was cancelled", true, err)
	}

	return lyrics, nil
}

func fetchExternalLyricsFromInternet(title string, artist string) (string, error) {
	baseURL := strings.TrimSpace(os.Getenv("LYRICS_API_BASE_URL"))
	if baseURL == "" {
		return "", newServiceError(ServiceErrorInternal, "LYRICS_API_BASE_URL is not set", false, nil)
	}

	normalizedBaseURL := strings.TrimRight(baseURL, "/")
	searchURL := fmt.Sprintf(
		"%s/search/get/web?s=%s&type=1&limit=1",
		normalizedBaseURL,
		url.QueryEscape(strings.TrimSpace(title)+" "+strings.TrimSpace(artist)),
	)

	searchStart := time.Now()
	searchResponse, err := http.Get(searchURL)
	if err != nil {
		return "", classifyProviderError(err)
	}
	defer searchResponse.Body.Close()

	searchBody, err := io.ReadAll(searchResponse.Body)
	if err != nil {
		return "", newServiceError(ServiceErrorUpstreamBadResp, "failed to read lyrics search response", false, err)
	}
	if searchResponse.StatusCode != http.StatusOK {
		return "", mapLyricsHTTPError(searchResponse.StatusCode, "lyrics source search")
	}

	var parsedSearch neteaseSearchResponse
	if err := json.Unmarshal(searchBody, &parsedSearch); err != nil {
		return "", newServiceError(ServiceErrorUpstreamBadResp, "lyrics source search returned invalid JSON", false, err)
	}
	if len(parsedSearch.Result.Songs) == 0 || parsedSearch.Result.Songs[0].ID == 0 {
		return "", newServiceError(ServiceErrorInvalidInput, "lyrics source could not find the requested song", false, nil)
	}
	log.Printf("[PERF] 1. NetEase Search ID took: %v", time.Since(searchStart))

	lyricURL := fmt.Sprintf(
		"%s/song/lyric?id=%d&lv=1&kv=1&tv=-1",
		normalizedBaseURL,
		parsedSearch.Result.Songs[0].ID,
	)

	lyricsFetchStart := time.Now()
	lyricResponse, err := http.Get(lyricURL)
	if err != nil {
		return "", classifyProviderError(err)
	}
	defer lyricResponse.Body.Close()

	lyricBody, err := io.ReadAll(lyricResponse.Body)
	if err != nil {
		return "", newServiceError(ServiceErrorUpstreamBadResp, "failed to read lyrics detail response", false, err)
	}
	if lyricResponse.StatusCode != http.StatusOK {
		return "", mapLyricsHTTPError(lyricResponse.StatusCode, "lyrics source lyric fetch")
	}

	var parsedLyric neteaseLyricResponse
	if err := json.Unmarshal(lyricBody, &parsedLyric); err != nil {
		return "", newServiceError(ServiceErrorUpstreamBadResp, "lyrics source lyric fetch returned invalid JSON", false, err)
	}
	log.Printf("[PERF] 2. NetEase Fetch Lyrics took: %v", time.Since(lyricsFetchStart))

	cleanStart := time.Now()
	cleanLyrics := cleanNetEaseLyrics(parsedLyric.LRC.Lyric)
	log.Printf("[PERF] 3. Regexp Clean took: %v", time.Since(cleanStart))
	if cleanLyrics == "" {
		return "", newServiceError(ServiceErrorUpstreamBadResp, "lyrics source returned empty lyrics", false, nil)
	}

	return cleanLyrics, nil
}

func cleanNetEaseLyrics(rawLyrics string) string {
	cleanedLines := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(rawLyrics, "\r\n", "\n"), "\n") {
		line = lyricTimestampPattern.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if containsHanPattern.MatchString(line) {
			continue
		}
		if !containsLatinPattern.MatchString(line) {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	return strings.Join(cleanedLines, "\n")
}

func mapLyricsHTTPError(statusCode int, operation string) error {
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound:
		return newServiceError(ServiceErrorInvalidInput, operation+" could not find the requested song", false, fmt.Errorf("status %d", statusCode))
	case http.StatusTooManyRequests:
		return newServiceError(ServiceErrorRateLimited, operation+" is rate limiting requests; please retry shortly", true, fmt.Errorf("status %d", statusCode))
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return newServiceError(ServiceErrorUpstreamTimeout, operation+" timed out", true, fmt.Errorf("status %d", statusCode))
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusInternalServerError:
		return newServiceError(ServiceErrorUpstreamUnavailable, operation+" is temporarily unavailable", true, fmt.Errorf("status %d", statusCode))
	default:
		return newServiceError(ServiceErrorUpstreamBadResp, operation+" returned an unexpected response", false, fmt.Errorf("status %d", statusCode))
	}
}
