package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ChartGranularity string

type ChartRangePreset string

type ChartQueryOptions struct {
	Granularity ChartGranularity `json:"granularity"`
	Range       ChartRangePreset `json:"range"`
	Start       time.Time        `json:"start"`
	End         time.Time        `json:"end"`
}

const (
	ChartGranularityMinute        ChartGranularity = "minute"
	ChartGranularityFifteenMinute ChartGranularity = "15-minutes"
	ChartGranularityHourly        ChartGranularity = "hourly"
	ChartGranularityDaily         ChartGranularity = "daily"
	ChartGranularityWeekly        ChartGranularity = "weekly"
	ChartGranularityMonthly       ChartGranularity = "monthly"
)

const (
	ChartRangeLast10Minutes ChartRangePreset = "last-10-minutes"
	ChartRangeLast15Minutes ChartRangePreset = "last-15-minutes"
	ChartRangeLast30Minutes ChartRangePreset = "last-30-minutes"
	ChartRangeLast60Minutes ChartRangePreset = "last-60-minutes"
	ChartRangeLastHour      ChartRangePreset = "last-hour"
	ChartRangeLast3Hours    ChartRangePreset = "last-3-hours"
	ChartRangeLast8Hours    ChartRangePreset = "last-8-hours"
	ChartRangeLast24Hours   ChartRangePreset = "last-24-hours"
	ChartRangeLast24Days    ChartRangePreset = "last-24-days"
	ChartRangeCustom        ChartRangePreset = "custom"
)

type chartAggregateRow struct {
	StartedAt     time.Time
	StatusCode    int
	RequestBytes  int64
	ResponseBytes int64
}

type chartBucket struct {
	Key   string
	Label string
	Start time.Time
	End   time.Time
}

func defaultChartQueryOptions(now time.Time) ChartQueryOptions {
	now = now.UTC().Truncate(time.Minute)
	return ChartQueryOptions{
		Granularity: ChartGranularityHourly,
		Range:       ChartRangeLast24Hours,
		Start:       now.Add(-24 * time.Hour),
		End:         now,
	}
}

func parseChartQueryOptions(r *http.Request) (ChartQueryOptions, error) {
	options := defaultChartQueryOptions(time.Now().UTC())
	query := r.URL.Query()

	if value := strings.TrimSpace(query.Get("granularity")); value != "" {
		granularity, err := parseChartGranularity(value)
		if err != nil {
			return ChartQueryOptions{}, err
		}
		options.Granularity = granularity
	}

	if value := strings.TrimSpace(query.Get("range")); value != "" {
		rangePreset, err := parseChartRangePreset(value)
		if err != nil {
			return ChartQueryOptions{}, err
		}
		options.Range = rangePreset
	}

	now := time.Now().UTC().Truncate(time.Minute)
	if options.Range == ChartRangeCustom {
		startValue := strings.TrimSpace(query.Get("start"))
		endValue := strings.TrimSpace(query.Get("end"))
		if startValue == "" || endValue == "" {
			return ChartQueryOptions{}, fmt.Errorf("custom chart range requires start and end")
		}
		start, err := parseChartTimestamp(startValue)
		if err != nil {
			return ChartQueryOptions{}, fmt.Errorf("invalid chart start: %w", err)
		}
		end, err := parseChartTimestamp(endValue)
		if err != nil {
			return ChartQueryOptions{}, fmt.Errorf("invalid chart end: %w", err)
		}
		if !start.Before(end) {
			return ChartQueryOptions{}, fmt.Errorf("chart start must be before chart end")
		}
		options.Start = start.UTC()
		options.End = end.UTC()
		return options, nil
	}

	start, end, err := chartRangeBounds(options.Range, now)
	if err != nil {
		return ChartQueryOptions{}, err
	}
	options.Start = start
	options.End = end
	return options, nil
}

func parseChartGranularity(value string) (ChartGranularity, error) {
	switch ChartGranularity(strings.TrimSpace(strings.ToLower(value))) {
	case ChartGranularityMinute,
		ChartGranularityFifteenMinute,
		ChartGranularityHourly,
		ChartGranularityDaily,
		ChartGranularityWeekly,
		ChartGranularityMonthly:
		return ChartGranularity(strings.TrimSpace(strings.ToLower(value))), nil
	default:
		return "", fmt.Errorf("unsupported chart granularity %q", value)
	}
}

func parseChartRangePreset(value string) (ChartRangePreset, error) {
	switch ChartRangePreset(strings.TrimSpace(strings.ToLower(value))) {
	case ChartRangeLast10Minutes,
		ChartRangeLast15Minutes,
		ChartRangeLast30Minutes,
		ChartRangeLast60Minutes,
		ChartRangeLastHour,
		ChartRangeLast3Hours,
		ChartRangeLast8Hours,
		ChartRangeLast24Hours,
		ChartRangeLast24Days,
		ChartRangeCustom:
		return ChartRangePreset(strings.TrimSpace(strings.ToLower(value))), nil
	default:
		return "", fmt.Errorf("unsupported chart range %q", value)
	}
}

func parseChartTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format")
}

func chartRangeBounds(preset ChartRangePreset, now time.Time) (time.Time, time.Time, error) {
	now = now.UTC().Truncate(time.Minute)
	switch preset {
	case ChartRangeLast10Minutes:
		return now.Add(-10 * time.Minute), now, nil
	case ChartRangeLast15Minutes:
		return now.Add(-15 * time.Minute), now, nil
	case ChartRangeLast30Minutes:
		return now.Add(-30 * time.Minute), now, nil
	case ChartRangeLast60Minutes, ChartRangeLastHour:
		return now.Add(-1 * time.Hour), now, nil
	case ChartRangeLast3Hours:
		return now.Add(-3 * time.Hour), now, nil
	case ChartRangeLast8Hours:
		return now.Add(-8 * time.Hour), now, nil
	case ChartRangeLast24Hours:
		return now.Add(-24 * time.Hour), now, nil
	case ChartRangeLast24Days:
		return now.AddDate(0, 0, -24), now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported chart range %q", preset)
	}
}

func (s *Store) loadChartAggregateRows(ctx context.Context, tunnelID string, options ChartQueryOptions) ([]chartAggregateRow, error) {
	query := `
		SELECT started_at, status_code, request_bytes, response_bytes
		FROM request_response_logs
		WHERE started_at >= ? AND started_at < ?
	`
	args := []any{options.Start.UTC(), options.End.UTC()}
	if strings.TrimSpace(tunnelID) != "" {
		query += ` AND tunnel_id = ?`
		args = append(args, tunnelID)
	}
	query += ` ORDER BY started_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("loading chart rows failed: %w", err)
	}
	defer rows.Close()

	items := make([]chartAggregateRow, 0)
	for rows.Next() {
		var item chartAggregateRow
		var startedAt sql.NullTime
		if err := rows.Scan(&startedAt, &item.StatusCode, &item.RequestBytes, &item.ResponseBytes); err != nil {
			return nil, fmt.Errorf("scanning chart row failed: %w", err)
		}
		if !startedAt.Valid {
			continue
		}
		item.StartedAt = startedAt.Time.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating chart rows failed: %w", err)
	}
	return items, nil
}

func (s *Store) statusChart(ctx context.Context, tunnelID string, options ChartQueryOptions) ([]StatusChartPoint, error) {
	rows, err := s.loadChartAggregateRows(ctx, tunnelID, options)
	if err != nil {
		return nil, err
	}
	buckets := buildChartBuckets(options)
	points := make([]StatusChartPoint, 0, len(buckets))
	index := make(map[string]int, len(buckets))
	for i, bucket := range buckets {
		points = append(points, StatusChartPoint{Bucket: bucket.Label})
		index[bucket.Key] = i
	}
	for _, row := range rows {
		key := chartBucketKey(truncateChartTime(row.StartedAt, options.Granularity))
		idx, ok := index[key]
		if !ok {
			continue
		}
		switch {
		case row.StatusCode >= 200 && row.StatusCode <= 299:
			points[idx].TwoXX++
		case row.StatusCode >= 300 && row.StatusCode <= 399:
			points[idx].ThreeXX++
		case row.StatusCode >= 400 && row.StatusCode <= 499:
			points[idx].FourXX++
		case row.StatusCode >= 500 && row.StatusCode <= 599:
			points[idx].FiveXX++
		}
	}
	return points, nil
}

func (s *Store) trafficChart(ctx context.Context, tunnelID string, options ChartQueryOptions) ([]TrafficChartPoint, error) {
	rows, err := s.loadChartAggregateRows(ctx, tunnelID, options)
	if err != nil {
		return nil, err
	}
	buckets := buildChartBuckets(options)
	points := make([]TrafficChartPoint, 0, len(buckets))
	index := make(map[string]int, len(buckets))
	for i, bucket := range buckets {
		points = append(points, TrafficChartPoint{Bucket: bucket.Label})
		index[bucket.Key] = i
	}
	for _, row := range rows {
		key := chartBucketKey(truncateChartTime(row.StartedAt, options.Granularity))
		idx, ok := index[key]
		if !ok {
			continue
		}
		points[idx].InboundBytes += row.RequestBytes
		points[idx].OutboundBytes += row.ResponseBytes
	}
	return points, nil
}

func buildChartBuckets(options ChartQueryOptions) []chartBucket {
	start := truncateChartTime(options.Start.UTC(), options.Granularity)
	end := options.End.UTC()
	if !start.Before(end) {
		end = nextChartBucketStart(start, options.Granularity)
	}
	window := end.Sub(start)
	buckets := make([]chartBucket, 0)
	for current := start; current.Before(end); current = nextChartBucketStart(current, options.Granularity) {
		next := nextChartBucketStart(current, options.Granularity)
		buckets = append(buckets, chartBucket{
			Key:   chartBucketKey(current),
			Label: chartBucketLabel(current, options.Granularity, window),
			Start: current,
			End:   next,
		})
	}
	return buckets
}

func truncateChartTime(value time.Time, granularity ChartGranularity) time.Time {
	value = value.UTC()
	switch granularity {
	case ChartGranularityMinute:
		return value.Truncate(time.Minute)
	case ChartGranularityFifteenMinute:
		minute := (value.Minute() / 15) * 15
		return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), minute, 0, 0, time.UTC)
	case ChartGranularityHourly:
		return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), 0, 0, 0, time.UTC)
	case ChartGranularityDaily:
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	case ChartGranularityWeekly:
		weekday := int(value.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := value.AddDate(0, 0, -(weekday - 1))
		return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	case ChartGranularityMonthly:
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return value.Truncate(time.Hour)
	}
}

func nextChartBucketStart(value time.Time, granularity ChartGranularity) time.Time {
	value = truncateChartTime(value, granularity)
	switch granularity {
	case ChartGranularityMinute:
		return value.Add(time.Minute)
	case ChartGranularityFifteenMinute:
		return value.Add(15 * time.Minute)
	case ChartGranularityHourly:
		return value.Add(time.Hour)
	case ChartGranularityDaily:
		return value.AddDate(0, 0, 1)
	case ChartGranularityWeekly:
		return value.AddDate(0, 0, 7)
	case ChartGranularityMonthly:
		return value.AddDate(0, 1, 0)
	default:
		return value.Add(time.Hour)
	}
}

func chartBucketKey(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func chartBucketLabel(value time.Time, granularity ChartGranularity, window time.Duration) string {
	value = value.UTC()
	switch granularity {
	case ChartGranularityMinute, ChartGranularityFifteenMinute:
		if window <= 24*time.Hour {
			return value.Format("15:04")
		}
		return value.Format("02 Jan 15:04")
	case ChartGranularityHourly:
		if window <= 24*time.Hour {
			return value.Format("15:04")
		}
		return value.Format("02 Jan 15:04")
	case ChartGranularityDaily:
		return value.Format("02 Jan")
	case ChartGranularityWeekly:
		return "Week of " + value.Format("02 Jan")
	case ChartGranularityMonthly:
		return value.Format("Jan 2006")
	default:
		return value.Format(time.RFC3339)
	}
}
