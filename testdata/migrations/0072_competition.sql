-- Competition domain (map #18, task #31) — Go test-schema mirror of
-- ytci-tanstack/src/db/migrations/0037_competition.sql.
--
-- Additive only: twelve new tables, their indexes and constraints. The legal
-- route is undecided at the domain layer, so promotion type, ages, dates, rules
-- version and public vote mode are configuration. The defaults encode the current Route B choice
-- (award programme, People's Choice only, no prizes) without making it the only
-- supported route.
--
-- Apply with the normal migration runner. Do not run against production without
-- a snapshot and a rehearsal.

CREATE TABLE IF NOT EXISTS "competition_editions" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"slug" text NOT NULL,
	"name" text NOT NULL,
	"theme" text,
	"status" text DEFAULT 'draft' NOT NULL,
	"promotion_type" text DEFAULT 'award_programme' NOT NULL,
	"min_age" integer,
	"max_age" integer,
	"public_vote_mode" text DEFAULT 'people_choice_only' NOT NULL,
	"prize_enabled" boolean DEFAULT false NOT NULL,
	"rules_version" text NOT NULL,
	"opens_at" timestamp,
	"closes_at" timestamp,
	"voting_opens_at" timestamp,
	"voting_closes_at" timestamp,
	"announcement_at" timestamp,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_editions_status_check" CHECK ("competition_editions"."status" in ('draft', 'consultation', 'open', 'judging', 'voting', 'closed', 'published')),
	CONSTRAINT "competition_editions_promotion_type_check" CHECK ("competition_editions"."promotion_type" in ('prize_competition', 'award_programme', 'licensed_operator')),
	CONSTRAINT "competition_editions_vote_mode_check" CHECK ("competition_editions"."public_vote_mode" in ('weighted', 'people_choice_only', 'none')),
	CONSTRAINT "competition_editions_age_range_check" CHECK ("competition_editions"."min_age" is null or "competition_editions"."max_age" is null or "competition_editions"."min_age" <= "competition_editions"."max_age"),
	CONSTRAINT "competition_editions_min_age_check" CHECK ("competition_editions"."min_age" is null or "competition_editions"."min_age" >= 0),
	CONSTRAINT "competition_editions_max_age_check" CHECK ("competition_editions"."max_age" is null or "competition_editions"."max_age" >= 0),
	CONSTRAINT "competition_editions_window_check" CHECK ("competition_editions"."opens_at" is null or "competition_editions"."closes_at" is null or "competition_editions"."opens_at" <= "competition_editions"."closes_at"),
	CONSTRAINT "competition_editions_voting_window_check" CHECK ("competition_editions"."voting_opens_at" is null or "competition_editions"."voting_closes_at" is null or "competition_editions"."voting_opens_at" <= "competition_editions"."voting_closes_at")
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_editions_slug_idx" ON "competition_editions" USING btree ("slug");
CREATE INDEX IF NOT EXISTS "competition_editions_status_opens_idx" ON "competition_editions" USING btree ("status","opens_at");

CREATE TABLE IF NOT EXISTS "competition_categories" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"key" text NOT NULL,
	"label" text NOT NULL,
	"description" text,
	"sort_order" integer DEFAULT 0 NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_categories_edition_key_idx" ON "competition_categories" USING btree ("edition_id","key");
CREATE INDEX IF NOT EXISTS "competition_categories_edition_sort_idx" ON "competition_categories" USING btree ("edition_id","sort_order");
ALTER TABLE "competition_categories" ADD CONSTRAINT "competition_categories_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_entries" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"category_id" uuid NOT NULL,
	"repository_entry_id" uuid NOT NULL,
	"lead_user_id" text,
	"team_name" text,
	"team_members" jsonb,
	"eligibility_status" text DEFAULT 'pending' NOT NULL,
	"eligibility_note" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_entries_eligibility_check" CHECK ("competition_entries"."eligibility_status" in ('pending', 'eligible', 'ineligible'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_entries_edition_category_repository_idx" ON "competition_entries" USING btree ("edition_id","category_id","repository_entry_id");
CREATE UNIQUE INDEX IF NOT EXISTS "competition_entries_edition_category_lead_idx" ON "competition_entries" USING btree ("edition_id","category_id","lead_user_id");
CREATE INDEX IF NOT EXISTS "competition_entries_edition_category_idx" ON "competition_entries" USING btree ("edition_id","category_id");
CREATE INDEX IF NOT EXISTS "competition_entries_edition_eligibility_idx" ON "competition_entries" USING btree ("edition_id","eligibility_status");
CREATE INDEX IF NOT EXISTS "competition_entries_repository_idx" ON "competition_entries" USING btree ("repository_entry_id");
ALTER TABLE "competition_entries" ADD CONSTRAINT "competition_entries_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_entries" ADD CONSTRAINT "competition_entries_category_id_competition_categories_id_fk" FOREIGN KEY ("category_id") REFERENCES "public"."competition_categories"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_entries" ADD CONSTRAINT "competition_entries_repository_entry_id_repository_entries_id_fk" FOREIGN KEY ("repository_entry_id") REFERENCES "public"."repository_entries"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_entries" ADD CONSTRAINT "competition_entries_lead_user_id_users_id_fk" FOREIGN KEY ("lead_user_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_judges" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"user_id" text,
	"display_name" text NOT NULL,
	"email" text,
	"status" text DEFAULT 'invited' NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_judges_status_check" CHECK ("competition_judges"."status" in ('invited', 'active', 'recused', 'inactive'))
);

CREATE INDEX IF NOT EXISTS "competition_judges_edition_idx" ON "competition_judges" USING btree ("edition_id","status");
CREATE INDEX IF NOT EXISTS "competition_judges_user_idx" ON "competition_judges" USING btree ("user_id");
ALTER TABLE "competition_judges" ADD CONSTRAINT "competition_judges_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_judges" ADD CONSTRAINT "competition_judges_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_judge_assignments" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"category_id" uuid NOT NULL,
	"judge_id" uuid NOT NULL,
	"status" text DEFAULT 'assigned' NOT NULL,
	"assigned_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_judge_assignments_status_check" CHECK ("competition_judge_assignments"."status" in ('assigned', 'recused', 'completed'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_judge_assignments_category_judge_idx" ON "competition_judge_assignments" USING btree ("category_id","judge_id");
CREATE INDEX IF NOT EXISTS "competition_judge_assignments_edition_idx" ON "competition_judge_assignments" USING btree ("edition_id","status");
CREATE INDEX IF NOT EXISTS "competition_judge_assignments_judge_idx" ON "competition_judge_assignments" USING btree ("judge_id");
ALTER TABLE "competition_judge_assignments" ADD CONSTRAINT "competition_judge_assignments_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_judge_assignments" ADD CONSTRAINT "competition_judge_assignments_category_id_competition_categories_id_fk" FOREIGN KEY ("category_id") REFERENCES "public"."competition_categories"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_judge_assignments" ADD CONSTRAINT "competition_judge_assignments_judge_id_competition_judges_id_fk" FOREIGN KEY ("judge_id") REFERENCES "public"."competition_judges"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_conflicts" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"judge_id" uuid NOT NULL,
	"entry_id" uuid NOT NULL,
	"reason" text NOT NULL,
	"declared_at" timestamp DEFAULT now() NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_conflicts_judge_entry_idx" ON "competition_conflicts" USING btree ("judge_id","entry_id");
CREATE INDEX IF NOT EXISTS "competition_conflicts_entry_idx" ON "competition_conflicts" USING btree ("entry_id");
ALTER TABLE "competition_conflicts" ADD CONSTRAINT "competition_conflicts_judge_id_competition_judges_id_fk" FOREIGN KEY ("judge_id") REFERENCES "public"."competition_judges"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_conflicts" ADD CONSTRAINT "competition_conflicts_entry_id_competition_entries_id_fk" FOREIGN KEY ("entry_id") REFERENCES "public"."competition_entries"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_scores" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"assignment_id" uuid NOT NULL,
	"entry_id" uuid NOT NULL,
	"judge_id" uuid NOT NULL,
	"relevance" integer NOT NULL,
	"craft" integer NOT NULL,
	"originality" integer NOT NULL,
	"impact" integer NOT NULL,
	"total" numeric(6, 2) NOT NULL,
	"notes" text,
	"locked_at" timestamp,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_scores_relevance_check" CHECK ("competition_scores"."relevance" between 1 and 10),
	CONSTRAINT "competition_scores_craft_check" CHECK ("competition_scores"."craft" between 1 and 10),
	CONSTRAINT "competition_scores_originality_check" CHECK ("competition_scores"."originality" between 1 and 10),
	CONSTRAINT "competition_scores_impact_check" CHECK ("competition_scores"."impact" between 1 and 10),
	CONSTRAINT "competition_scores_total_check" CHECK ("competition_scores"."total" between 0 and 100)
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_scores_assignment_entry_idx" ON "competition_scores" USING btree ("assignment_id","entry_id");
CREATE INDEX IF NOT EXISTS "competition_scores_entry_idx" ON "competition_scores" USING btree ("entry_id");
CREATE INDEX IF NOT EXISTS "competition_scores_judge_idx" ON "competition_scores" USING btree ("judge_id");
ALTER TABLE "competition_scores" ADD CONSTRAINT "competition_scores_assignment_id_competition_judge_assignments_id_fk" FOREIGN KEY ("assignment_id") REFERENCES "public"."competition_judge_assignments"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_scores" ADD CONSTRAINT "competition_scores_entry_id_competition_entries_id_fk" FOREIGN KEY ("entry_id") REFERENCES "public"."competition_entries"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_scores" ADD CONSTRAINT "competition_scores_judge_id_competition_judges_id_fk" FOREIGN KEY ("judge_id") REFERENCES "public"."competition_judges"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_votes" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"category_id" uuid NOT NULL,
	"entry_id" uuid NOT NULL,
	"voter_user_id" text NOT NULL,
	"idempotency_key" text NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"invalidated_at" timestamp,
	"invalidated_by" text,
	"reason_code" text
);

CREATE UNIQUE INDEX IF NOT EXISTS "competition_votes_voter_entry_idx" ON "competition_votes" USING btree ("voter_user_id","entry_id");
CREATE UNIQUE INDEX IF NOT EXISTS "competition_votes_idempotency_idx" ON "competition_votes" USING btree ("idempotency_key");
CREATE INDEX IF NOT EXISTS "competition_votes_counts_idx" ON "competition_votes" USING btree ("edition_id","category_id","entry_id");
CREATE INDEX IF NOT EXISTS "competition_votes_voter_edition_idx" ON "competition_votes" USING btree ("voter_user_id","edition_id");
CREATE INDEX IF NOT EXISTS "competition_votes_entry_live_idx" ON "competition_votes" USING btree ("entry_id","invalidated_at");
ALTER TABLE "competition_votes" ADD CONSTRAINT "competition_votes_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_votes" ADD CONSTRAINT "competition_votes_category_id_competition_categories_id_fk" FOREIGN KEY ("category_id") REFERENCES "public"."competition_categories"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_votes" ADD CONSTRAINT "competition_votes_entry_id_competition_entries_id_fk" FOREIGN KEY ("entry_id") REFERENCES "public"."competition_entries"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_votes" ADD CONSTRAINT "competition_votes_voter_user_id_users_id_fk" FOREIGN KEY ("voter_user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_vote_review_cases" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"entry_id" uuid,
	"signal" text NOT NULL,
	"status" text DEFAULT 'open' NOT NULL,
	"opened_at" timestamp DEFAULT now() NOT NULL,
	"resolved_at" timestamp,
	"resolution" text,
	"reviewer_id" text,
	CONSTRAINT "competition_vote_review_cases_status_check" CHECK ("competition_vote_review_cases"."status" in ('open', 'reviewing', 'resolved', 'dismissed'))
);

CREATE INDEX IF NOT EXISTS "competition_vote_review_cases_edition_status_idx" ON "competition_vote_review_cases" USING btree ("edition_id","status");
CREATE INDEX IF NOT EXISTS "competition_vote_review_cases_entry_idx" ON "competition_vote_review_cases" USING btree ("entry_id");
ALTER TABLE "competition_vote_review_cases" ADD CONSTRAINT "competition_vote_review_cases_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_vote_review_cases" ADD CONSTRAINT "competition_vote_review_cases_entry_id_competition_entries_id_fk" FOREIGN KEY ("entry_id") REFERENCES "public"."competition_entries"("id") ON DELETE set null ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_leaderboard_snapshots" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"category_id" uuid,
	"payload" jsonb NOT NULL,
	"ledger_hash" text NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"is_final" boolean DEFAULT false NOT NULL
);

CREATE INDEX IF NOT EXISTS "competition_leaderboard_snapshots_edition_idx" ON "competition_leaderboard_snapshots" USING btree ("edition_id","category_id","created_at");
CREATE INDEX IF NOT EXISTS "competition_leaderboard_snapshots_final_idx" ON "competition_leaderboard_snapshots" USING btree ("edition_id","category_id","is_final");
ALTER TABLE "competition_leaderboard_snapshots" ADD CONSTRAINT "competition_leaderboard_snapshots_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_leaderboard_snapshots" ADD CONSTRAINT "competition_leaderboard_snapshots_category_id_competition_categories_id_fk" FOREIGN KEY ("category_id") REFERENCES "public"."competition_categories"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_awards" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid NOT NULL,
	"category_id" uuid,
	"entry_id" uuid NOT NULL,
	"award_type" text NOT NULL,
	"award_form" text,
	"citation" text,
	"published_at" timestamp,
	"created_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "competition_awards_type_check" CHECK ("competition_awards"."award_type" in ('category_winner', 'overall', 'people_choice')),
	CONSTRAINT "competition_awards_form_check" CHECK ("competition_awards"."award_form" is null or "competition_awards"."award_form" in ('grant', 'commission', 'certificate', 'feature', 'recognition'))
);

CREATE INDEX IF NOT EXISTS "competition_awards_edition_type_idx" ON "competition_awards" USING btree ("edition_id","award_type");
CREATE INDEX IF NOT EXISTS "competition_awards_entry_idx" ON "competition_awards" USING btree ("entry_id");
ALTER TABLE "competition_awards" ADD CONSTRAINT "competition_awards_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_awards" ADD CONSTRAINT "competition_awards_category_id_competition_categories_id_fk" FOREIGN KEY ("category_id") REFERENCES "public"."competition_categories"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "competition_awards" ADD CONSTRAINT "competition_awards_entry_id_competition_entries_id_fk" FOREIGN KEY ("entry_id") REFERENCES "public"."competition_entries"("id") ON DELETE cascade ON UPDATE no action;

CREATE TABLE IF NOT EXISTS "competition_consultation_responses" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"edition_id" uuid,
	"name" text,
	"email" text,
	"answers" jsonb NOT NULL,
	"consent" boolean DEFAULT false NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL
);

CREATE INDEX IF NOT EXISTS "competition_consultation_responses_edition_idx" ON "competition_consultation_responses" USING btree ("edition_id","created_at");
ALTER TABLE "competition_consultation_responses" ADD CONSTRAINT "competition_consultation_responses_edition_id_competition_editions_id_fk" FOREIGN KEY ("edition_id") REFERENCES "public"."competition_editions"("id") ON DELETE set null ON UPDATE no action;
