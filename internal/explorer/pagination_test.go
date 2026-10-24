package explorer_test

import (
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
)

func TestParsePagination(t *testing.T) {
	tests := []struct {
		name       string
		pageStr    string
		sizeStr    string
		wantPage   int
		wantSize   int
		wantOffset int
	}{
		{"defaults on empty", "", "", 1, 50, 0},
		{"defaults on invalid string", "abc", "xyz", 1, 50, 0},
		{"negative values clamped", "-5", "-10", 1, 50, 0},
		{"zero page clamped to 1", "0", "20", 1, 20, 0},
		{"page 2 with size 25", "2", "25", 2, 25, 25},
		{"page 3 with size 50", "3", "50", 3, 50, 100},
		{"size exceeding max clamped to 200", "1", "500", 1, 200, 0},
		{"max valid size", "5", "200", 5, 200, 800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explorer.ParsePagination(tt.pageStr, tt.sizeStr)
			if got.Page != tt.wantPage {
				t.Errorf("Page: got %d, want %d", got.Page, tt.wantPage)
			}
			if got.Size != tt.wantSize {
				t.Errorf("Size: got %d, want %d", got.Size, tt.wantSize)
			}
			if got.Offset() != tt.wantOffset {
				t.Errorf("Offset: got %d, want %d", got.Offset(), tt.wantOffset)
			}
		})
	}
}
