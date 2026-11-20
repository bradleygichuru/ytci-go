package contracts_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
	"github.com/bradleygichuru/ytci-go/internal/db"
)

// TestMigrationsApplyOnFreshDatabase is the definition-of-done check for the
// schema slice. It runs the whole testdata/migrations directory in order on a
// fresh PostGIS database, then asserts the contract-bearing constraints.
func TestMigrationsApplyOnFreshDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers migration check in -short mode")
	}
	pool := db.SetupTestDB(t)
	ctx := context.Background()

	t.Run("tables", func(t *testing.T) {
		for _, table := range []string{
			"venues", "venue_accessibility", "organizer_profiles",
			"organizer_verification_events", "organizer_staff",
			"event_sessions", "event_people", "session_speakers",
			"event_registrations", "event_ticket_handoffs", "event_tickets", "event_checkins",
			"event_checkin_scans", "event_reports", "event_notifications",
			"event_change_notices", "event_accessibility",
			"recognition.events", "recognition.event_types", "recognition.rules",
			"recognition.badge_definitions", "recognition.badge_awards",
			"recognition.xp_entries", "recognition.xp_totals", "recognition.levels",
			"recognition.seasons", "recognition.leaderboard_entries",
			"recognition.outbox", "recognition.audit", "recognition.kill_switches",
			"species", "species_names", "species_content", "species_media",
			"species_audio", "species_sources", "species_rights",
			"species_where_to_see", "species_links", "species_reviews",
			"content_languages",
			"analytics_installs", "analytics_sessions", "analytics_event_schemas",
			"analytics_daily", "analytics_journey_daily", "analytics_retention_cohorts",
			"analytics_consents", "analytics_erasure_requests",
			"analytics_service_diagnostics", "platform_contract_versions",
			"repository_entries", "repository_entry_media",
			"repository_upload_intents", "repository_settings",
			"repository_categories",
		} {
			var exists *string
			if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&exists); err != nil {
				t.Fatalf("to_regclass(%s): %v", table, err)
			}
			if exists == nil {
				t.Fatalf("missing table %s", table)
			}
		}
	})

	t.Run("cloud_event_registry", func(t *testing.T) {
		rows, err := pool.Query(ctx, `SELECT type FROM recognition.event_types ORDER BY type`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			got = append(got, name)
		}
		want := append([]string(nil), contracts.CloudEventTypes...)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("event_types = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("event_types = %v, want %v", got, want)
			}
		}
	})

	t.Run("repository_catalogue", func(t *testing.T) {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO repository_entries (kind, slug, title, summary, tags, status)
			VALUES ('innovation', 'solar-cold-storage', 'Solar cold storage',
			        'A youth cooling project', ARRAY['conservation','technology'], 'published')
			RETURNING id`).Scan(&id); err != nil {
			t.Fatalf("insert repository entry: %v", err)
		}
		var hits int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM repository_entries
			WHERE search_vector @@ to_tsquery('english', 'conserv')`).Scan(&hits); err != nil {
			t.Fatalf("search_vector query: %v", err)
		}
		if hits != 1 {
			t.Fatalf("search_vector hits = %d, want 1", hits)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO repository_entries (kind, slug, title, summary)
			VALUES ('innovation', 'solar-cold-storage', 'Duplicate', 'Duplicate slug')`); err == nil {
			t.Fatal("duplicate repository slug must be rejected")
		}
		var categories, settings int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM repository_categories`).Scan(&categories); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM repository_settings`).Scan(&settings); err != nil {
			t.Fatal(err)
		}
		if categories != 23 || settings != 1 {
			t.Fatalf("repository seed rows = (%d categories, %d settings), want (23, 1)", categories, settings)
		}
	})

	t.Run("levels_and_switches", func(t *testing.T) {
		rows, err := pool.Query(ctx, `SELECT threshold FROM recognition.levels ORDER BY level`)
		if err != nil {
			t.Fatal(err)
		}
		var thresholds []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			thresholds = append(thresholds, v)
		}
		rows.Close()
		want := []int{0, 250, 750, 1500, 3000}
		if len(thresholds) != len(want) {
			t.Fatalf("levels = %v, want %v", thresholds, want)
		}
		for i := range want {
			if thresholds[i] != want[i] {
				t.Fatalf("levels = %v, want %v", thresholds, want)
			}
		}
		var switches int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM recognition.kill_switches`).Scan(&switches); err != nil {
			t.Fatal(err)
		}
		if switches != 4 {
			t.Fatalf("kill switches = %d, want 4", switches)
		}
	})

	t.Run("contract_versions", func(t *testing.T) {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_contract_versions WHERE version = '1.0.0'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 6 {
			t.Fatalf("contract versions = %d, want 6", count)
		}
	})

	t.Run("content_languages", func(t *testing.T) {
		for _, lang := range []string{"en", "sw", "ki", "luo", "kam", "mas", "so"} {
			var tier string
			if err := pool.QueryRow(ctx, `SELECT coverage_tier FROM content_languages WHERE tag = $1`, lang).Scan(&tier); err != nil {
				t.Fatalf("language %s: %v", lang, err)
			}
		}
	})

	t.Run("role_check_matches_contract", func(t *testing.T) {
		var def string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'users_role_check'`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		for _, role := range contracts.MustRoles().PublicRoles() {
			if !strings.Contains(def, "'"+role+"'") {
				t.Fatalf("users_role_check does not allow role %q: %s", role, def)
			}
		}
	})

	t.Run("event_lifecycle", func(t *testing.T) {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO events (title, organizer, county, event_date, type, status)
			VALUES ('Contract event', 'Org', 'Nairobi', current_date, 'cultural', 'published')
			RETURNING id`).Scan(&id); err != nil {
			t.Fatalf("insert published event: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO events (title, organizer, county, event_date, type, status)
			VALUES ('Legacy event', 'Org', 'Nairobi', current_date, 'cultural', 'scheduled')`); err != nil {
			t.Fatalf("scheduled must remain a legacy alias: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO events (title, organizer, county, event_date, type, status)
			VALUES ('Bad event', 'Org', 'Nairobi', current_date, 'cultural', 'completed')`); err == nil {
			t.Fatal("status 'completed' must be rejected (derived, never stored)")
		}
		// Two questions are allowed; the third is rejected by the trigger.
		for i := 0; i < 2; i++ {
			if _, err := pool.Exec(ctx, `
				INSERT INTO event_registration_questions (event_id, prompt_en, display_order)
				VALUES ($1, 'Question', $2)`, id, i); err != nil {
				t.Fatalf("question %d: %v", i, err)
			}
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO event_registration_questions (event_id, prompt_en, display_order)
			VALUES ($1, 'Third question', 3)`, id); err == nil {
			t.Fatal("a third registration question must be rejected")
		}
	})

	t.Run("organizer_verification", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organizer_profiles (slug, display_name, verification_status)
			VALUES ('bad-verified', 'Bad', 'verified')`); err == nil {
			t.Fatal("verified organizer requires a trust tier and verified_at")
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO organizer_profiles (slug, display_name, trust_tier, verification_status, verified_at)
			VALUES ('good-verified', 'Good', 'verified_organizer', 'verified', now())`); err != nil {
			t.Fatalf("valid verified organizer rejected: %v", err)
		}
	})

	t.Run("xp_ledger_is_append_only", func(t *testing.T) {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO recognition.xp_entries
				(subject_id, amount, reason, rule_key, rule_version, source_type, source_id)
			VALUES ('reader-1', 50, 'award', 'event.attendance', 1, 'event', 'event-1')
			RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE recognition.xp_entries SET amount = 500 WHERE id = $1`, id); err == nil {
			t.Fatal("amount updates must be rejected")
		}
		if _, err := pool.Exec(ctx, `UPDATE recognition.xp_entries SET subject_id = 'anonymised' WHERE id = $1`, id); err != nil {
			t.Fatalf("subject anonymisation must be allowed: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM recognition.xp_entries WHERE id = $1`, id); err == nil {
			t.Fatal("deletes must be rejected")
		}
	})

	t.Run("analytics_identity_is_pseudonymous", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics_events (event, analytics_id)
			VALUES ('screen_view', gen_random_uuid())`); err != nil {
			t.Fatalf("analytics_id-only row rejected: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO analytics_events (event) VALUES ('screen_view')`); err == nil {
			t.Fatal("row with neither user_id nor analytics_id must be rejected")
		}
		var cols []string
		rows, err := pool.Query(ctx, `
			SELECT column_name FROM information_schema.columns
			WHERE table_name = 'analytics_events' AND column_name IN
			  ('analytics_id','session_id','session_seq','event_family','client_ts','received_at','server_seq','actor_type','route')`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		rows.Close()
		if len(cols) != 9 {
			t.Fatalf("analytics envelope columns = %v", cols)
		}
	})

	t.Run("role_check_rejects_unknown_role", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, name, email, role) VALUES ('u-wizard','Wizard','wizard@example.ke','wizard')`); err == nil {
			t.Fatal("unknown role must be rejected for new rows")
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, name, email, role) VALUES ('u-translator','Translator','translator@example.ke','translator')`); err != nil {
			t.Fatalf("contract role rejected: %v", err)
		}
	})
}

func TestMigrationRangesAreOrdered(t *testing.T) {
	// The ranges are documented in docs/execution/contracts.md; this keeps the
	// stream blocks predictable for the feature branches.
	ranges := []struct {
		stream string
		from   int
		to     int
	}{
		{"mice", 17, 29},
		{"recognition", 30, 42},
		{"species", 43, 55},
		{"analytics", 56, 68},
		{"platform", 69, 79},
		{"verification", 80, 89},
	}
	for i := 1; i < len(ranges); i++ {
		if ranges[i].from <= ranges[i-1].to {
			t.Fatalf("ranges overlap: %+v", ranges)
		}
	}
}
