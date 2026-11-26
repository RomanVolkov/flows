package analytics

import (
	"hash/fnv"
	"slices"
	"strings"

	"github.com/romanvolkov/flows/internal/usage"
)

var modelPalette = []string{
	"#89B4FA", "#A6E3A1", "#F9E2AF", "#CBA6F7",
	"#94E2D5", "#F38BA8", "#F5C2E7", "#F59E0B",
}

// ModelColor returns a deterministic hex color for model.
func ModelColor(model string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(model))
	return modelPalette[int(h.Sum32())%len(modelPalette)]
}

// ChartPoint is one date/value point on a model series.
type ChartPoint struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}

// ChartSeries represents one model's daily token series.
type ChartSeries struct {
	Model string       `json:"model"`
	Color string       `json:"color"`
	Total int64        `json:"total"`
	Points []ChartPoint `json:"points"`
}

// TableRow is one exact-values row matching the daily chart.
type TableRow struct {
	Date          string `json:"date"`
	Harness       string `json:"harness"`
	Model         string `json:"model"`
	KnownSubtotal int64  `json:"known_subtotal"`
	Uncached      int64  `json:"uncached"`
	CacheRead     int64  `json:"cache_read"`
	Output        int64  `json:"output"`
	Reasoning     int64  `json:"reasoning"`
	Coverage      string `json:"coverage"`
}

// DailyView holds the chart series, exact-values table, and hover crosshair state.
type DailyView struct {
	Range      RangePreset   `json:"range"`
	Series     []ChartSeries `json:"series"`
	Table      []TableRow    `json:"table"`
	HoverIndex int           `json:"hover_index"`
}

// BuildDailyView constructs a synchronized chart and exact-values table from daily buckets.
func BuildDailyView(rng RangePreset, buckets []usage.DailyBucket, harnessFilter, modelFilter string) DailyView {
	view := DailyView{
		Range:      rng,
		HoverIndex: -1,
	}

	byModel := make(map[string]*ChartSeries)
	for _, b := range buckets {
		if harnessFilter != "" && b.Harness != harnessFilter {
			continue
		}
		if modelFilter != "" && b.Model != modelFilter {
			continue
		}
		view.Table = append(view.Table, TableRow{
			Date:          b.Date,
			Harness:       b.Harness,
			Model:         b.Model,
			KnownSubtotal: b.KnownSubtotal,
			Uncached:      b.UncachedInputTokens,
			CacheRead:     b.CacheReadTokens,
			Output:        b.OutputTokens,
			Reasoning:     b.ReasoningTokens,
			Coverage:      b.Coverage,
		})
		s, ok := byModel[b.Model]
		if !ok {
			s = &ChartSeries{Model: b.Model, Color: ModelColor(b.Model)}
			byModel[b.Model] = s
		}
		s.Total += b.KnownSubtotal
		s.Points = append(s.Points, ChartPoint{Date: b.Date, Value: b.KnownSubtotal})
	}

	for _, s := range byModel {
		view.Series = append(view.Series, *s)
	}
	slices.SortFunc(view.Series, func(a, b ChartSeries) int {
		return strings.Compare(a.Model, b.Model)
	})
	if len(view.Table) > 0 {
		view.HoverIndex = 0
	}
	return view
}

// MoveCrosshair moves the keyboard/hover crosshair across table rows.
func (v *DailyView) MoveCrosshair(delta int) *TableRow {
	if len(v.Table) == 0 {
		v.HoverIndex = -1
		return nil
	}
	if v.HoverIndex < 0 {
		v.HoverIndex = 0
	} else {
		v.HoverIndex = (v.HoverIndex + delta + len(v.Table)) % len(v.Table)
	}
	return &v.Table[v.HoverIndex]
}
