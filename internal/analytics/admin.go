package analytics

import (
	"context"
	"sort"
	"time"
)

// Overview reads the overview aggregates. Admin surfaces read rollup tables
// only; they never scan analytics_events.
func (s *Store) Overview(ctx context.Context, from, to time.Time) (Overview, error) {
	out := Overview{
		From:  from.Format("2006-01-02"),
		To:    to.Format("2006-01-02"),
		Cells: []MetricCell{},
		Daily: []DailyPoint{},
	}

	rows, err := s.pool.Query(ctx, `
		SELECT metric, dimension_key, value, suppressed, COALESCE(sample_size, 0),
		       COALESCE(dimension, '{}'::jsonb)
		FROM analytics_daily
		WHERE day >= $1::date AND day <= $2::date
		ORDER BY day`, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	type cellRow struct {
		value      float64
		suppressed bool
		sample     int
	}
	latest := map[string]cellRow{}
	daily := map[string]*DailyPoint{}
	for rows.Next() {
		var metric, dimKey string
		var value float64
		var suppressed bool
		var sample int
		var dim []byte
		if err := rows.Scan(&metric, &dimKey, &value, &suppressed, &sample, &dim); err != nil {
			return out, err
		}
		latest[metric+"|"+dimKey] = cellRow{value, suppressed, sample}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	// Daily points need the day, so run a second small aggregate query.
	drows, err := s.pool.Query(ctx, `
		SELECT day::text, dimension_key, value, suppressed
		FROM analytics_daily
		WHERE metric = 'dau' AND day >= $1::date AND day <= $2::date
		ORDER BY day`, from, to)
	if err != nil {
		return out, err
	}
	defer drows.Close()
	for drows.Next() {
		var day, dimKey string
		var value float64
		var suppressed bool
		if err := drows.Scan(&day, &dimKey, &value, &suppressed); err != nil {
			return out, err
		}
		p := daily[day]
		if p == nil {
			p = &DailyPoint{Day: day, Suppressed: true}
			daily[day] = p
		}
		v := int64(value)
		switch dimKey {
		case "":
			p.DAU = &v
			p.Suppressed = suppressed || v < SuppressionThreshold
		case "actor=guest":
			p.Guests = &v
		case "actor=account":
			p.Accounts = &v
		}
	}
	if err := drows.Err(); err != nil {
		return out, err
	}

	cellSpecs := []struct {
		metric string
		unit   string
		count  bool
		dim    string
	}{
		{"new_accounts", "count", true, ""},
		{"dau", "count", true, ""},
		{"wau", "count", true, ""},
		{"mau", "count", true, ""},
		{"guest_to_account_ratio", "ratio", false, ""},
		{"activation_rate", "percent", false, ""},
	}
	for _, spec := range cellSpecs {
		cell := MetricCell{
			Metric:     spec.metric,
			Unit:       spec.unit,
			Definition: spec.metric,
			Suppressed: true,
		}
		if row, ok := latest[spec.metric+"|"+spec.dim]; ok {
			if row.suppressed || (spec.count && row.value < SuppressionThreshold) {
				cell.Suppressed = true
			} else {
				v := row.value
				cell.Value = &v
				cell.Suppressed = false
			}
		}
		out.Cells = append(out.Cells, cell)
	}

	days := make([]string, 0, len(daily))
	for day := range daily {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		out.Daily = append(out.Daily, *daily[day])
	}
	return out, nil
}

type journeyCell struct {
	journey   string
	step      string
	index     int
	reached   int64
	completed int64
	bucket    *string
}

// Journeys aggregates the journey rollup for a date range.
func (s *Store) Journeys(ctx context.Context, from, to time.Time, journey string) ([]JourneyStepMetric, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT journey, step, step_index, reached, completed, median_duration_bucket, suppressed, day
		FROM analytics_journey_daily
		WHERE day >= $1::date AND day <= $2::date
		  AND ($3 = '' OR journey = $3)
		ORDER BY day`, from, to, journey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cells := map[string]*journeyCell{}
	for rows.Next() {
		var c journeyCell
		var bucket *string
		var day time.Time
		if err := rows.Scan(&c.journey, &c.step, &c.index, &c.reached, &c.completed,
			&bucket, new(bool), &day); err != nil {
			return nil, err
		}
		key := c.journey + "|" + c.step
		existing := cells[key]
		if existing == nil {
			c.bucket = bucket
			cells[key] = &c
			continue
		}
		existing.reached += c.reached
		existing.completed += c.completed
		if bucket != nil {
			existing.bucket = bucket
		}
	}

	out := make([]JourneyStepMetric, 0, len(cells))
	byJourney := map[string][]*journeyCell{}
	for _, c := range cells {
		byJourney[c.journey] = append(byJourney[c.journey], c)
	}
	names := make([]string, 0, len(byJourney))
	for name := range byJourney {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		steps := byJourney[name]
		sort.Slice(steps, func(i, j int) bool { return steps[i].index < steps[j].index })
		for i, c := range steps {
			reached := c.reached
			var dropped *int64
			if i > 0 {
				d := steps[i-1].reached - c.reached
				if d < 0 {
					d = 0
				}
				dropped = &d
			}
			suppressed := c.reached < SuppressionThreshold
			var reachedPtr *int64
			if !suppressed {
				reachedPtr = &reached
			}
			out = append(out, JourneyStepMetric{
				Journey:              c.journey,
				Step:                 c.step,
				StepIndex:            c.index,
				Reached:              reachedPtr,
				Dropped:              dropped,
				MedianDurationBucket: c.bucket,
				Suppressed:           suppressed,
			})
		}
	}
	return out, nil
}

// Funnels derives one row per journey from the journey rollup.
func (s *Store) Funnels(ctx context.Context, from, to time.Time) ([]FunnelMetric, error) {
	steps, err := s.Journeys(ctx, from, to, "")
	if err != nil {
		return nil, err
	}
	type agg struct {
		entered, completed int64
		have               bool
		lastIndex          int
	}
	byJourney := map[string]*agg{}
	for _, step := range steps {
		if step.Reached == nil {
			continue
		}
		a := byJourney[step.Journey]
		if a == nil {
			a = &agg{lastIndex: -1}
			byJourney[step.Journey] = a
		}
		if !a.have {
			a.entered = *step.Reached
			a.have = true
		}
		if step.StepIndex >= a.lastIndex {
			a.lastIndex = step.StepIndex
			a.completed = *step.Reached
		}
	}
	names := make([]string, 0, len(byJourney))
	for name := range byJourney {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]FunnelMetric, 0, len(names))
	for _, name := range names {
		a := byJourney[name]
		row := FunnelMetric{Journey: name, Suppressed: true}
		if a.entered >= SuppressionThreshold {
			entered, completed := a.entered, a.completed
			row.Entered = &entered
			row.Completed = &completed
			row.Suppressed = false
			if entered > 0 {
				conversion := float64(completed) / float64(entered)
				row.Conversion = &conversion
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Retention reads the weekly retention cohorts.
func (s *Store) Retention(ctx context.Context, from, to time.Time) ([]RetentionMetric, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cohort_week::text, week_index, actors, retained, suppressed
		FROM analytics_retention_cohorts
		WHERE cohort_week >= $1::date AND cohort_week <= $2::date
		ORDER BY cohort_week, week_index`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RetentionMetric, 0)
	for rows.Next() {
		var week string
		var index int
		var actors, retained int64
		var suppressed bool
		if err := rows.Scan(&week, &index, &actors, &retained, &suppressed); err != nil {
			return nil, err
		}
		row := RetentionMetric{CohortWeek: week, WeekIndex: index, Suppressed: true}
		if !suppressed && actors >= SuppressionThreshold {
			a, r := actors, retained
			row.Actors = &a
			row.Retained = &r
			row.Suppressed = false
			if actors > 0 {
				rate := float64(retained) / float64(actors)
				row.Rate = &rate
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Adoption reads feature reach, depth and repeat from daily rollups.
func (s *Store) Adoption(ctx context.Context, from, to time.Time) ([]AdoptionMetric, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT metric, dimension_key, value, suppressed
		FROM analytics_daily
		WHERE day >= $1::date AND day <= $2::date
		  AND metric IN ('feature_reach','feature_depth','feature_repeat')
		ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type feature struct {
		reach, depth, repeat             float64
		suppressed                       bool
		haveReach, haveDepth, haveRepeat bool
	}
	features := map[string]*feature{}
	for rows.Next() {
		var metric, dimKey string
		var value float64
		var suppressed bool
		if err := rows.Scan(&metric, &dimKey, &value, &suppressed); err != nil {
			return nil, err
		}
		name := dimKey
		if len(name) > len("feature=") && name[:len("feature=")] == "feature=" {
			name = name[len("feature="):]
		}
		if name == "" {
			continue
		}
		f := features[name]
		if f == nil {
			f = &feature{}
			features[name] = f
		}
		switch metric {
		case "feature_reach":
			f.reach, f.suppressed, f.haveReach = value, suppressed, true
		case "feature_depth":
			f.depth, f.haveDepth = value, true
		case "feature_repeat":
			f.repeat, f.haveRepeat = value, true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]AdoptionMetric, 0, len(names))
	for _, name := range names {
		f := features[name]
		row := AdoptionMetric{Feature: name, Suppressed: true}
		if f.haveReach && !f.suppressed && f.reach >= SuppressionThreshold {
			reach := int64(f.reach)
			row.Reach = &reach
			row.Suppressed = false
			if f.haveDepth {
				depth := f.depth
				row.DepthPerActor = &depth
			}
			if f.haveRepeat {
				repeat := f.repeat
				row.RepeatRate = &repeat
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Quality reports ingest, consent and rollup health.
func (s *Store) Quality(ctx context.Context, from, to time.Time) (DataQuality, error) {
	out := DataQuality{
		From:        from.Format("2006-01-02"),
		To:          to.Format("2006-01-02"),
		Suppression: SuppressionQuality{Threshold: SuppressionThreshold},
	}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(event_count),0), COALESCE(sum(dropped_count),0),
		       COALESCE(sum(unknown_key_count),0),
		       count(*) FILTER (WHERE status = 'rejected')
		FROM analytics_ingest_batches
		WHERE received_at >= $1 AND received_at < $2`,
		from, to.AddDate(0, 0, 1)).Scan(
		&out.Ingest.Batches, &out.Ingest.Events, &out.Ingest.DroppedEvents,
		&out.Ingest.UnknownKeys, &out.Ingest.RejectedBatches)
	if err != nil {
		return out, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT state, count(*) FROM analytics_consents
		WHERE recorded_at >= $1 AND recorded_at < $2
		GROUP BY state`, from, to.AddDate(0, 0, 1))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			return out, err
		}
		switch state {
		case ConsentGranted:
			out.Consent.Granted = n
		case ConsentDenied:
			out.Consent.Denied = n
		default:
			out.Consent.Unknown = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM analytics_consents
		WHERE recorded_at >= $1 AND recorded_at < $2 AND version <> $3`,
		from, to.AddDate(0, 0, 1), ConsentVersion()).Scan(&out.Consent.StaleVersion); err != nil {
		return out, err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM analytics_daily
		WHERE day >= $1::date AND day <= $2::date AND suppressed`,
		from, to).Scan(&out.Suppression.CellsSuppressed); err != nil {
		return out, err
	}

	rrows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (rollup) rollup, day::text, status
		FROM analytics_rollup_runs
		ORDER BY rollup, day DESC`)
	if err != nil {
		return out, err
	}
	defer rrows.Close()
	out.Rollups = []RollupStatus{}
	for rrows.Next() {
		var r RollupStatus
		if err := rrows.Scan(&r.Rollup, &r.LastDay, &r.Status); err != nil {
			return out, err
		}
		out.Rollups = append(out.Rollups, r)
	}
	return out, rrows.Err()
}
