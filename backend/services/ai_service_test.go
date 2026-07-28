package services

import (
	"strings"
	"testing"
)

// TestSplitLyricsIntoChunks verifies the lyric chunking strategy used before
// sending data to the LLM. These tests do not call any real LLM, network, or
// database — splitLyricsIntoChunks is a pure function.
func TestSplitLyricsIntoChunks(t *testing.T) {
	tests := []struct {
		name              string
		lines             []string
		wantChunkCount    int
		wantLinesPerChunk []int // expected number of lines in each chunk (in order)
	}{
		{
			name:           "empty input returns single empty chunk",
			lines:          []string{},
			wantChunkCount: 1,
			wantLinesPerChunk: []int{0},
		},
		{
			name:           "short lyrics under both limits return single chunk",
			lines:          []string{"hello world", "this is a test", "short lyrics"},
			wantChunkCount: 1,
			wantLinesPerChunk: []int{3},
		},
		{
			name: "exactly 16 lines is the boundary for single chunk",
			lines: func() []string {
				lines := make([]string, 16)
				for i := range lines {
					lines[i] = "line " + string(rune('a'+i))
				}
				return lines
			}(),
			wantChunkCount:    1,
			wantLinesPerChunk: []int{16},
		},
		{
			name: "17 lines triggers chunking into two chunks (10 + 7)",
			lines: func() []string {
				lines := make([]string, 17)
				for i := range lines {
					lines[i] = "line " + string(rune('a'+i%26))
				}
				return lines
			}(),
			wantChunkCount:    2,
			wantLinesPerChunk: []int{10, 7},
		},
		{
			name: "single very long line exceeds char limit gets its own chunk",
			lines: []string{
				strings.Repeat("a", 950),
			},
			wantChunkCount:    1,
			wantLinesPerChunk: []int{1},
		},
		{
			name: "many short lines split into multiple chunks of at most 10 lines",
			lines: func() []string {
				lines := make([]string, 35)
				for i := range lines {
					lines[i] = "short"
				}
				return lines
			}(),
			wantChunkCount:    4,
			wantLinesPerChunk: []int{10, 10, 10, 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitLyricsIntoChunks(tt.lines)

			if len(chunks) != tt.wantChunkCount {
				t.Fatalf("got %d chunks, want %d", len(chunks), tt.wantChunkCount)
			}

			for i, chunk := range chunks {
				if i >= len(tt.wantLinesPerChunk) {
					t.Fatalf("got more chunks (%d) than expected (%d)", len(chunks), len(tt.wantLinesPerChunk))
				}
				gotLines := len(chunk.Lines)
				wantLines := tt.wantLinesPerChunk[i]
				if gotLines != wantLines {
					t.Errorf("chunk %d: got %d lines, want %d", i, gotLines, wantLines)
				}
			}

			// When input fits within singleCallMaxLines / singleCallMaxChars,
			// the function returns a single chunk that is allowed to be larger
			// than maxChunkLines (that limit only applies to multi-chunk mode).
			// For multi-chunk results, verify each chunk respects maxChunkLines.
			if len(chunks) > 1 {
				for i, chunk := range chunks {
					if len(chunk.Lines) > maxChunkLines {
						t.Errorf("chunk %d has %d lines, exceeds maxChunkLines (%d)", i, len(chunk.Lines), maxChunkLines)
					}
				}
			}

			// Verify all input lines are preserved across chunks (no data loss).
			totalInputLines := len(tt.lines)
			totalChunkLines := 0
			for _, chunk := range chunks {
				totalChunkLines += len(chunk.Lines)
			}
			if totalChunkLines != totalInputLines {
				t.Errorf("line count mismatch: input has %d lines, chunks contain %d lines total", totalInputLines, totalChunkLines)
			}
		})
	}
}

// TestSplitLyricsIntoChunksPreservesOrder verifies that the lines in the
// resulting chunks maintain the same order as the input.
func TestSplitLyricsIntoChunksPreservesOrder(t *testing.T) {
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = "line-" + string(rune('A'+i))
	}

	chunks := splitLyricsIntoChunks(lines)

	// Flatten chunks back into a single slice.
	var collected []string
	for _, chunk := range chunks {
		collected = append(collected, chunk.Lines...)
	}

	if len(collected) != len(lines) {
		t.Fatalf("flattened length %d != input length %d", len(collected), len(lines))
	}

	for i, want := range lines {
		if collected[i] != want {
			t.Errorf("line %d: got %q, want %q", i, collected[i], want)
		}
	}
}
