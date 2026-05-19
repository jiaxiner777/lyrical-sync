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
	Lines       []modelChunkLine `json:"l"`
	LegacyLines []modelChunkLine `json:"lines"`
}

type modelChunkLine struct {
	Text            string           `json:"t"`
	LegacyText      string           `json:"text"`
	OriginalText    string           `json:"originalText"`
	Words           []modelChunkUnit `json:"w"`
	LegacyWords     []modelChunkUnit `json:"words"`
	Syllables       []modelChunkUnit `json:"s"`
	LegacySyllables []modelChunkUnit `json:"syllables"`
	Details         []modelChunkUnit `json:"details"`
}

type modelChunkUnit struct {
	Text          string   `json:"t"`
	LegacyText    string   `json:"text"`
	Word          string   `json:"word"`
	Pinyin        string   `json:"p"`
	LegacyPinyin  string   `json:"pinyin"`
	Type          string   `json:"type,omitempty"`
	Elision       bool     `json:"elision,omitempty"`
	Link          bool     `json:"link,omitempty"`
	LinkWithNext  *bool    `json:"linkWithNext,omitempty"`
	Opacity       *float64 `json:"o,omitempty"`
	LegacyOpacity *float64 `json:"opacity,omitempty"`
}

func (response modelChunkResponse) effectiveLines() []modelChunkLine {
	if len(response.Lines) > 0 {
		return response.Lines
	}
	return response.LegacyLines
}

func (line modelChunkLine) units() []modelChunkUnit {
	if len(line.Syllables) > 0 {
		return line.Syllables
	}
	if len(line.LegacySyllables) > 0 {
		return line.LegacySyllables
	}
	if len(line.Words) > 0 {
		return line.Words
	}
	if len(line.LegacyWords) > 0 {
		return line.LegacyWords
	}
	return line.Details
}

func (unit modelChunkUnit) textValue() string {
	if strings.TrimSpace(unit.Text) != "" {
		return unit.Text
	}
	if strings.TrimSpace(unit.LegacyText) != "" {
		return unit.LegacyText
	}
	return unit.Word
}

func (unit modelChunkUnit) pinyinValue() string {
	if strings.TrimSpace(unit.Pinyin) != "" {
		return unit.Pinyin
	}
	return unit.LegacyPinyin
}

func (unit modelChunkUnit) opacityValue() *float64 {
	if unit.Opacity != nil {
		return unit.Opacity
	}
	return unit.LegacyOpacity
}

func (unit modelChunkUnit) hasLink() bool {
	if unit.Link {
		return true
	}
	return unit.LinkWithNext != nil && *unit.LinkWithNext
}

func restoreWordDetails(units []modelChunkUnit) ([]WordDetail, error) {
	details := make([]WordDetail, 0, len(units))
	for _, unit := range units {
		word := strings.TrimSpace(unit.textValue())
		pinyin := strings.TrimSpace(unit.pinyinValue())
		if shouldSkipModelUnit(word, pinyin) {
			continue
		}
		if word == "" || pinyin == "" {
			log.Printf("[WARN] AI invalid pronunciation unit. t=%q text=%q word=%q p=%q pinyin=%q type=%q", unit.Text, unit.LegacyText, unit.Word, unit.Pinyin, unit.LegacyPinyin, unit.Type)
			return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned an invalid pronunciation unit", true, nil)
		}

		detail := WordDetail{
			Word:   word,
			Pinyin: pinyin,
			Type:   "normal",
		}

		opacity := unit.opacityValue()
		isElision := unit.Elision || opacity != nil || strings.EqualFold(unit.Type, "elision")
		if unit.hasLink() {
			link := true
			detail.LinkWithNext = &link
			if !isElision {
				detail.Type = "liaison"
			}
		}
		if strings.EqualFold(unit.Type, "liaison") && !isElision {
			detail.Type = "liaison"
		}
		if isElision {
			detail.Type = "elision"
			if opacity != nil {
				detail.Opacity = opacity
			} else {
				defaultOpacity := 0.5
				detail.Opacity = &defaultOpacity
			}
		}

		details = append(details, detail)
	}

	if len(details) == 0 {
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned no usable pronunciation units", true, nil)
	}

	return details, nil
}

func shouldSkipModelUnit(word string, pinyin string) bool {
	if word == "" && pinyin == "" {
		return true
	}
	if pinyin != "" {
		return false
	}
	for _, r := range word {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type lyricChunk struct {
	Lines []string
}

var aiPromptTemplate = template.Must(template.New("syllable-json-system-prompt").Parse(`You are an expert English lyric pronunciation annotator for Chinese-speaking learners.

Your task is to annotate English lyric lines into ultra-compact pronunciation JSON.
Return JSON only. Do not wrap the response in markdown. Do not add explanations.

The JSON schema must be:
{
  "l": [
    {
      "t": "original lyric line",
      "s": [
        {
          "t": "token from the lyric line",
          "p": "Chinese-style pronunciation hint",
          "elision": true,
          "o": 0.5,
          "link": true
        }
      ]
    }
  ]
}

Rules you must follow:
1. Return one JSON object with key l.
2. Output line count in l must exactly equal input line count.
3. Do not merge, split, omit, summarize, abbreviate, or reorder lyric lines.
4. You must process the full input from the first lyric line to the last lyric line.
5. Never output placeholders such as "...", "same as above", "[Chorus repeats]", or any shortened form.
6. Every non-empty input lyric line must produce exactly one corresponding line object.
7. Each line object must include t for the full original lyric line and s for the spoken units in order.
8. Each spoken unit must include only t and p when the pronunciation is normal.
9. Never output any type field.
10. Only output elision:true when a sound is weakened, swallowed, or nearly omitted.
11. Only output o when elision:true is present. o must be between 0 and 1.
12. Only output link:true when the current spoken unit links smoothly into the next spoken unit.
13. Never output false boolean fields.
14. Preserve contractions such as I'd, don't, you're as single words when appropriate.
15. Keep p concise and learner-friendly for Chinese speakers.
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
  "l": [
    {
      "t": "I'd spend ten thousand hours or the rest of my life",
      "s": [
        { "t": "I'd", "p": "艾(d)", "elision": true, "o": 0.5 },
        { "t": "spend", "p": "斯班" },
        { "t": "ten", "p": "天" },
        { "t": "thousand", "p": "套-赞", "link": true },
        { "t": "hours", "p": "奥-儿", "link": true },
        { "t": "or", "p": "则" },
        { "t": "the", "p": "得" },
        { "t": "rest", "p": "热-斯-得" },
        { "t": "of", "p": "(夫)", "elision": true, "o": 0.3 },
        { "t": "my", "p": "麦" },
        { "t": "life", "p": "赖(夫)", "elision": true, "o": 0.5 }
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

	sanitizedJSON := sanitizeModelJSON(rawJSON)
	var parsed modelChunkResponse
	if err := json.Unmarshal([]byte(sanitizedJSON), &parsed); err != nil {
		log.Printf("[WARN] AI raw JSON parse failed. sample=%q", truncateForLog(sanitizedJSON, 800))
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned invalid JSON", true, err)
	}

	parsedLines := parsed.effectiveLines()
	if len(parsedLines) != len(chunk.Lines) {
		log.Printf("[WARN] AI line count mismatch. expected=%d got=%d sample=%q", len(chunk.Lines), len(parsedLines), truncateForLog(sanitizedJSON, 800))
		return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned a mismatched number of lyric lines", true, nil)
	}

	result := make([]SongLine, 0, len(chunk.Lines))
	for index, sourceLine := range chunk.Lines {
		annotatedLine := parsedLines[index]
		units := annotatedLine.units()
		if len(units) == 0 {
			log.Printf("[WARN] AI returned empty units for line sample=%q", truncateForLog(sanitizedJSON, 800))
			return nil, newServiceError(ServiceErrorUpstreamBadResp, "AI provider returned an empty pronunciation unit array for a lyric line", true, nil)
		}

		details, detailsErr := restoreWordDetails(units)
		if detailsErr != nil {
			return nil, detailsErr
		}

		lineText := strings.TrimSpace(annotatedLine.Text)
		if lineText == "" {
			lineText = strings.TrimSpace(annotatedLine.LegacyText)
		}
		if lineText == "" {
			lineText = strings.TrimSpace(annotatedLine.OriginalText)
		}
		if lineText == "" {
			lineText = sourceLine
		}

		result = append(result, SongLine{
			OriginalText: lineText,
			Details:      details,
		})
	}

	return result, nil
}

func buildChunkUserPrompt(chunk lyricChunk) string {
	return fmt.Sprintf(
		"Annotate exactly %d lyric lines in the same order. Return one JSON object with key l only. Prefer compact keys l, s, t, p, o, elision, link. Do not add any extra commentary.\n\nLyrics:\n%s",
		len(chunk.Lines),
		strings.Join(chunk.Lines, "\n"),
	)
}

func sanitizeModelJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(raw[start : end+1])
	}

	return raw
}

func truncateForLog(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
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
