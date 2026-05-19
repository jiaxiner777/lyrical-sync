package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const (
	singleCallMaxLines = 16
	singleCallMaxChars = 1200
	maxChunkLines      = 10
	maxChunkChars      = 900
	maxLyricsLines     = 120
	maxLyricsChars     = 8000
	perChunkTimeout    = 25 * time.Second
	maxChunkRetries    = 3
)

type ServiceErrorKind string

const (
	ServiceErrorInvalidInput        ServiceErrorKind = "invalid_input"
	ServiceErrorTooLarge            ServiceErrorKind = "too_large"
	ServiceErrorUpstreamTimeout     ServiceErrorKind = "upstream_timeout"
	ServiceErrorUpstreamBadResp     ServiceErrorKind = "upstream_bad_response"
	ServiceErrorUpstreamUnavailable ServiceErrorKind = "upstream_unavailable"
	ServiceErrorRateLimited         ServiceErrorKind = "rate_limited"
	ServiceErrorInternal            ServiceErrorKind = "internal"
)

type ServiceError struct {
	Kind      ServiceErrorKind
	Message   string
	Retryable bool
	Err       error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type SongSyllableData struct {
	SongID string     `json:"songId"`
	Lines  []SongLine `json:"lines"`
}

type SongLine struct {
	StartTime    float64      `json:"startTime"`
	EndTime      float64      `json:"endTime"`
	OriginalText string       `json:"originalText"`
	Details      []WordDetail `json:"details"`
}

type WordDetail struct {
	Word         string   `json:"word"`
	Pinyin       string   `json:"pinyin"`
	Type         string   `json:"type"`
	Opacity      *float64 `json:"opacity,omitempty"`
	LinkWithNext *bool    `json:"linkWithNext,omitempty"`
}

type promptTemplateData struct {
	ExampleLyrics string
	ExampleJSON   string
}

type modelChunkResponse struct {
	Lines []modelChunkLine `json:"lines"`
}

type modelChunkLine struct {
	OriginalText string       `json:"originalText"`
	Details      []WordDetail `json:"details"`
}

type lyricChunk struct {
	Lines []string
}

var aiPromptTemplate = template.Must(template.New("syllable-json-system-prompt").Parse(`You are an expert English lyric pronunciation annotator for Chinese-speaking learners.

Your task is to annotate English lyric lines into syllable-level pronunciation hints.
Return JSON only. Do not wrap the response in markdown. Do not add explanations.

The JSON schema must be:
{
  "lines": [
    {
      "originalText": "original lyric line",
      "details": [
        {
          "word": "token from the lyric line",
          "pinyin": "Chinese-style pronunciation hint",
          "type": "normal | liaison | elision",
          "opacity": 0.5,
          "linkWithNext": true
        }
      ]
    }
  ]
}

Rules you must follow:
1. Return one JSON object with key lines.
2. Output line count must exactly equal input line count.
3. Do not merge, split, omit, summarize, abbreviate, or reorder lyric lines.
4. You must process the full input from the first lyric line to the last lyric line.
5. Never output placeholders such as "...", "same as above", "[Chorus repeats]", or any shortened form.
6. Every non-empty input lyric line must produce exactly one corresponding JSON line entry.
7. Do not include songId, startTime, or endTime. The backend will add those fields.
8. details must preserve the spoken word order from originalText.
9. Use type="normal" for standard pronunciation.
10. Use type="elision" when a sound is weakened, swallowed, or nearly omitted. Include opacity between 0 and 1 for elision items.
11. Use type="liaison" when the current word links smoothly into the next word. Set linkWithNext=true on the current word.
12. Omit opacity unless the item is elision.
13. Omit linkWithNext unless it is true.
14. Preserve contractions such as I'd, don't, you're as single words when appropriate.
15. Keep pinyin concise and learner-friendly for Chinese speakers.
16. The output must be valid JSON that can be parsed directly with a JSON parser.

Few-shot example input:
{{.ExampleLyrics}}

Few-shot example output:
{{.ExampleJSON}}
`))

func GenerateFullSongJSON(ctx context.Context, lyrics string) (*SongSyllableData, error) {
	normalizedLyrics := strings.TrimSpace(strings.ReplaceAll(lyrics, "\r\n", "\n"))
	if normalizedLyrics == "" {
		return nil, newServiceError(ServiceErrorInvalidInput, "lyrics is required", false, nil)
	}

	lines := extractNonEmptyLines(normalizedLyrics)
	if len(lines) == 0 {
		return nil, newServiceError(ServiceErrorInvalidInput, "lyrics is required", false, nil)
	}
	if len(lines) > maxLyricsLines || len(normalizedLyrics) > maxLyricsChars {
		return nil, newServiceError(ServiceErrorTooLarge, "lyrics are too large for synchronous parsing; please shorten them or split the song into sections", false, nil)
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, newServiceError(ServiceErrorInternal, "OPENAI_API_KEY is not set", false, nil)
	}

	model := strings.TrimSpace(os.Getenv("MODEL_NAME"))
	if model == "" {
		model = "deepseek-chat"
	}

	baseURL := strings.TrimSpace(os.Getenv("API_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}

	systemPrompt, err := renderSystemPrompt()
	if err != nil {
		return nil, newServiceError(ServiceErrorInternal, "failed to build AI prompt", false, err)
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	config.HTTPClient = &http.Client{Timeout: 120 * time.Second}
	client := openai.NewClientWithConfig(config)

	chunks := splitLyricsIntoChunks(lines)
	llmStart := time.Now()
	mergedLines := make([]SongLine, 0, len(lines))
	for _, chunk := range chunks {
		annotatedLines, chunkErr := annotateChunkWithRetry(ctx, client, model, systemPrompt, chunk)
		if chunkErr != nil {
			return nil, chunkErr
		}
		mergedLines = append(mergedLines, annotatedLines...)
	}
	log.Printf("[PERF] 4. DeepSeek LLM Generation took: %v", time.Since(llmStart))

	return &SongSyllableData{
		SongID: generateSongID(lines),
		Lines:  assignPlaceholderTimings(mergedLines),
	}, nil
}

func renderSystemPrompt() (string, error) {
	data := promptTemplateData{
		ExampleLyrics: "I'd spend ten thousand hours or the rest of my life",
		ExampleJSON: `{
  "lines": [
    {
      "originalText": "I'd spend ten thousand hours or the rest of my life",
      "details": [
        { "word": "I'd", "pinyin": "艾(d)", "type": "elision", "opacity": 0.5 },
        { "word": "spend", "pinyin": "斯班", "type": "normal" },
        { "word": "ten", "pinyin": "天", "type": "normal" },
        { "word": "thousand", "pinyin": "套-赞", "type": "liaison", "linkWithNext": true },
        { "word": "hours", "pinyin": "奥-儿", "type": "liaison", "linkWithNext": true },
        { "word": "or", "pinyin": "则", "type": "normal" },
        { "word": "the", "pinyin": "得", "type": "normal" },
        { "word": "rest", "pinyin": "热-斯-得", "type": "normal" },
        { "word": "of", "pinyin": "(夫)", "type": "elision", "opacity": 0.3 },
        { "word": "my", "pinyin": "麦", "type": "normal" },
        { "word": "life", "pinyin": "赖(夫)", "type": "elision", "opacity": 0.5 }
      ]
    }
  ]
}`,
	}

	var buffer bytes.Buffer
	if err := aiPromptTemplate.Execute(&buffer, data); err != nil {
		return "", err
	}

	return buffer.String(), nil
}

func splitLyricsIntoChunks(lines []string) []lyricChunk {
	if len(lines) <= singleCallMaxLines && joinedLength(lines) <= singleCallMaxChars {
		return []lyricChunk{{Lines: lines}}
	}

	chunks := make([]lyricChunk, 0)
	current := make([]string, 0, maxChunkLines)

	flushCurrent := func() {
		if len(current) == 0 {
			return
		}
		chunkLines := append([]string(nil), current...)
		chunks = append(chunks, lyricChunk{Lines: chunkLines})
		current = current[:0]
	}

	for _, line := range lines {
		candidate := append(current, line)
		if len(candidate) > maxChunkLines || joinedLength(candidate) > maxChunkChars {
			flushCurrent()
			current = append(current, line)
			continue
		}
		current = candidate
	}
	flushCurrent()

	return chunks
}

func annotateChunkWithRetry(
	ctx context.Context,
	client *openai.Client,
	model string,
	systemPrompt string,
	chunk lyricChunk,
) ([]SongLine, error) {
	var lastErr error
	for attempt := 0; attempt < maxChunkRetries; attempt++ {
		annotatedLines, err := annotateChunkOnce(ctx, client, model, systemPrompt, chunk)
		if err == nil {
			return annotatedLines, nil
		}
		lastErr = err

		var serviceErr *ServiceError
		if !errors.As(err, &serviceErr) || !serviceErr.Retryable || attempt == maxChunkRetries-1 {
			return nil, err
		}

		backoff := time.Duration(attempt+1) * time.Second
		if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
			return nil, newServiceError(ServiceErrorUpstreamTimeout, "lyric parsing was cancelled before retry could complete", true, sleepErr)
		}
	}

	return nil, lastErr
}

func annotateChunkOnce(
	ctx context.Context,
	client *openai.Client,
	model string,
	systemPrompt string,
	chunk lyricChunk,
) ([]SongLine, error) {
	chunkCtx, cancel := context.WithTimeout(ctx, perChunkTimeout)
	defer cancel()

	prompt := buildChunkUserPrompt(chunk)
	response, err := client.CreateChatCompletion(chunkCtx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, classifyProviderError(err)
	}

	if len(response.Choices) == 0 {
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned no choices", false, nil)
	}

	rawJSON := strings.TrimSpace(response.Choices[0].Message.Content)
	if rawJSON == "" {
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned empty JSON content", false, nil)
	}

	var parsed modelChunkResponse
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned invalid JSON", false, err)
	}
	if len(parsed.Lines) != len(chunk.Lines) {
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned a mismatched number of lyric lines", false, nil)
	}

	result := make([]SongLine, 0, len(chunk.Lines))
	for index, sourceLine := range chunk.Lines {
		annotatedLine := parsed.Lines[index]
		if len(annotatedLine.Details) == 0 {
			return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned an empty details array for a lyric line", false, nil)
		}

		result = append(result, SongLine{
			OriginalText: sourceLine,
			Details:      annotatedLine.Details,
		})
	}

	return result, nil
}

func buildChunkUserPrompt(chunk lyricChunk) string {
	return fmt.Sprintf(
		"Annotate exactly %d lyric lines in the same order. Return one JSON object with key lines only. Do not add any extra commentary.\n\nLyrics:\n%s",
		len(chunk.Lines),
		strings.Join(chunk.Lines, "\n"),
	)
}

func assignPlaceholderTimings(lines []SongLine) []SongLine {
	cursor := 0.0
	result := make([]SongLine, 0, len(lines))
	for _, line := range lines {
		duration := math.Max(2.5, float64(len(line.Details))*0.65)
		startTime := roundToTwoDecimals(cursor)
		endTime := roundToTwoDecimals(cursor + duration)
		cursor = endTime
		result = append(result, SongLine{
			StartTime:    startTime,
			EndTime:      endTime,
			OriginalText: line.OriginalText,
			Details:      line.Details,
		})
	}
	return result
}

func generateSongID(lines []string) string {
	if len(lines) == 0 {
		return "untitled_song"
	}

	source := strings.ToLower(strings.Join(lines[:min(len(lines), 2)], " "))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := strings.Trim(re.ReplaceAllString(source, "_"), "_")
	if slug == "" {
		return "untitled_song"
	}
	parts := strings.Split(slug, "_")
	if len(parts) > 6 {
		parts = parts[:6]
	}
	return strings.Join(parts, "_")
}

func classifyProviderError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newServiceError(ServiceErrorUpstreamTimeout, "AI provider timed out while parsing the lyrics", true, err)
	}
	if errors.Is(err, context.Canceled) {
		return newServiceError(ServiceErrorUpstreamTimeout, "lyric parsing request was cancelled", true, err)
	}

	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 408, 504:
			return newServiceError(ServiceErrorUpstreamTimeout, "AI provider timed out while parsing the lyrics", true, err)
		case 429:
			return newServiceError(ServiceErrorRateLimited, "AI provider is rate limiting requests; please retry shortly", true, err)
		case 500, 502, 503:
			return newServiceError(ServiceErrorUpstreamUnavailable, "AI provider is temporarily unavailable", true, err)
		case 400:
			return newServiceError(ServiceErrorInvalidInput, "AI provider rejected the lyric request", false, err)
		case 401, 403:
			return newServiceError(ServiceErrorInternal, "AI provider authentication failed", false, err)
		default:
			return newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned an unexpected error", false, err)
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newServiceError(ServiceErrorUpstreamTimeout, "AI provider timed out while parsing the lyrics", true, err)
	}

	return newServiceError(ServiceErrorInternal, "failed to call AI provider", false, err)
}

func extractNonEmptyLines(lyrics string) []string {
	rawLines := strings.Split(lyrics, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func joinedLength(lines []string) int {
	return len(strings.Join(lines, "\n"))
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func roundToTwoDecimals(value float64) float64 {
	return math.Round(value*100) / 100
}

func newServiceError(kind ServiceErrorKind, message string, retryable bool, err error) *ServiceError {
	return &ServiceError{
		Kind:      kind,
		Message:   message,
		Retryable: retryable,
		Err:       err,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
