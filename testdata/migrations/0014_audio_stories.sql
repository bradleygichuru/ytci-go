CREATE TABLE "audio_stories" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"slug" text NOT NULL,
	"title" text NOT NULL,
	"subtitle" text,
	"summary" text NOT NULL,
	"theme" text NOT NULL,
	"tags" text[] DEFAULT '{}'::text[] NOT NULL,
	"cover_media_id" uuid,
	"producer" text NOT NULL,
	"community_source" text NOT NULL,
	"recorded_at" date,
	"collection_id" uuid,
	"collection_order" integer DEFAULT 0 NOT NULL,
	"featured" boolean DEFAULT false NOT NULL,
	"featured_order" integer,
	"status" text DEFAULT 'draft' NOT NULL,
	"live_revision_id" uuid,
	"open_revision_id" uuid,
	"content_version" integer DEFAULT 0 NOT NULL,
	"first_published_at" timestamp,
	"unpublished_at" timestamp,
	"archived_at" timestamp,
	"revoked_at" timestamp,
	"created_by" text,
	"updated_by" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "audio_stories_slug_unique" UNIQUE("slug")
);
--> statement-breakpoint
CREATE TABLE "audio_story_collections" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"slug" text NOT NULL,
	"title" text NOT NULL,
	"description" text NOT NULL,
	"cover_media_id" uuid,
	"status" text DEFAULT 'draft' NOT NULL,
	"display_order" integer DEFAULT 0 NOT NULL,
	"featured" boolean DEFAULT false NOT NULL,
	"featured_order" integer,
	"publish_at" timestamp,
	"first_published_at" timestamp,
	"created_by" text,
	"updated_by" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "audio_story_collections_slug_unique" UNIQUE("slug")
);
--> statement-breakpoint
CREATE TABLE "audio_story_events" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"story_id" uuid NOT NULL,
	"revision_id" uuid,
	"track_id" uuid,
	"collection_id" uuid,
	"actor_id" text,
	"action" text NOT NULL,
	"from_status" text,
	"to_status" text,
	"reason" text,
	"metadata" jsonb,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "audio_story_reviews" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"revision_id" uuid NOT NULL,
	"reviewer_id" text,
	"external_reviewer_name" text,
	"external_reviewer_affiliation" text,
	"decision" text NOT NULL,
	"risk_tier" text NOT NULL,
	"notes" text,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "audio_story_revisions" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"story_id" uuid NOT NULL,
	"status" text DEFAULT 'draft' NOT NULL,
	"content_version" integer DEFAULT 0 NOT NULL,
	"place_scope" text NOT NULL,
	"counties" text[] DEFAULT '{}'::text[] NOT NULL,
	"destination_ids" uuid[] DEFAULT '{}'::uuid[] NOT NULL,
	"content_note_categories" text[] DEFAULT '{}'::text[] NOT NULL,
	"content_note_text" text,
	"rights_basis" text NOT NULL,
	"rights_holder" text NOT NULL,
	"licence_reference" text,
	"rights_expires_at" timestamp,
	"risk_tier" text DEFAULT 'standard' NOT NULL,
	"attested_by" text,
	"attested_at" timestamp,
	"assigned_reviewer_id" text,
	"second_reviewer_id" text,
	"submitted_at" timestamp,
	"approved_at" timestamp,
	"publish_at" timestamp,
	"published_at" timestamp,
	"revoked_at" timestamp,
	"revocation_reason" text,
	"created_by" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "audio_story_tracks" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"revision_id" uuid NOT NULL,
	"language" text NOT NULL,
	"media_asset_id" uuid,
	"audio_object_key" text NOT NULL,
	"duration_seconds" integer NOT NULL,
	"narrator" text NOT NULL,
	"narrator_consent_date" date,
	"narrator_consent_reference" text,
	"size_bytes" bigint,
	"format" text,
	"bitrate_kbps" integer,
	"recorded_at" date,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "audio_story_transcripts" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"revision_id" uuid NOT NULL,
	"language" text NOT NULL,
	"paragraphs" jsonb NOT NULL,
	"reviewed_at" timestamp,
	"reviewed_by" text,
	"source" text DEFAULT 'human',
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "county_code" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "interest_tags" text[] DEFAULT '{}'::text[] NOT NULL;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "transport_modes" text[] DEFAULT '{}'::text[] NOT NULL;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "best_seasons" text[] DEFAULT '{}'::text[] NOT NULL;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "is_curated" boolean DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "visit_duration_min_minutes" integer;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "visit_duration_max_minutes" integer;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "fee_status" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "fee_currency" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "fee_amount" integer;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "fee_basis" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "opening_hours_status" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "opening_hours" jsonb;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "hero_image_url" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "hero_image_attribution" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "hero_image_license" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "hero_image_source" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "hero_image_alt" text;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "verified_at" timestamp;--> statement-breakpoint
ALTER TABLE "destinations" ADD COLUMN "verified_by" text;--> statement-breakpoint
ALTER TABLE "kenya_counties" ADD COLUMN "county_code" text NOT NULL DEFAULT '';--> statement-breakpoint
ALTER TABLE "audio_stories" ADD CONSTRAINT "audio_stories_cover_media_id_media_assets_id_fk" FOREIGN KEY ("cover_media_id") REFERENCES "public"."media_assets"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_stories" ADD CONSTRAINT "audio_stories_collection_id_audio_story_collections_id_fk" FOREIGN KEY ("collection_id") REFERENCES "public"."audio_story_collections"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_stories" ADD CONSTRAINT "audio_stories_created_by_users_id_fk" FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_stories" ADD CONSTRAINT "audio_stories_updated_by_users_id_fk" FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_collections" ADD CONSTRAINT "audio_story_collections_cover_media_id_media_assets_id_fk" FOREIGN KEY ("cover_media_id") REFERENCES "public"."media_assets"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_collections" ADD CONSTRAINT "audio_story_collections_created_by_users_id_fk" FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_collections" ADD CONSTRAINT "audio_story_collections_updated_by_users_id_fk" FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_events" ADD CONSTRAINT "audio_story_events_actor_id_users_id_fk" FOREIGN KEY ("actor_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_reviews" ADD CONSTRAINT "audio_story_reviews_revision_id_audio_story_revisions_id_fk" FOREIGN KEY ("revision_id") REFERENCES "public"."audio_story_revisions"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_reviews" ADD CONSTRAINT "audio_story_reviews_reviewer_id_users_id_fk" FOREIGN KEY ("reviewer_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_revisions" ADD CONSTRAINT "audio_story_revisions_story_id_audio_stories_id_fk" FOREIGN KEY ("story_id") REFERENCES "public"."audio_stories"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_revisions" ADD CONSTRAINT "audio_story_revisions_attested_by_users_id_fk" FOREIGN KEY ("attested_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_revisions" ADD CONSTRAINT "audio_story_revisions_assigned_reviewer_id_users_id_fk" FOREIGN KEY ("assigned_reviewer_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_revisions" ADD CONSTRAINT "audio_story_revisions_second_reviewer_id_users_id_fk" FOREIGN KEY ("second_reviewer_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_revisions" ADD CONSTRAINT "audio_story_revisions_created_by_users_id_fk" FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_tracks" ADD CONSTRAINT "audio_story_tracks_revision_id_audio_story_revisions_id_fk" FOREIGN KEY ("revision_id") REFERENCES "public"."audio_story_revisions"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_tracks" ADD CONSTRAINT "audio_story_tracks_media_asset_id_media_assets_id_fk" FOREIGN KEY ("media_asset_id") REFERENCES "public"."media_assets"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_transcripts" ADD CONSTRAINT "audio_story_transcripts_revision_id_audio_story_revisions_id_fk" FOREIGN KEY ("revision_id") REFERENCES "public"."audio_story_revisions"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "audio_story_transcripts" ADD CONSTRAINT "audio_story_transcripts_reviewed_by_users_id_fk" FOREIGN KEY ("reviewed_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "audio_stories_status_updated_idx" ON "audio_stories" USING btree ("status","updated_at");--> statement-breakpoint
CREATE INDEX "audio_stories_collection_order_idx" ON "audio_stories" USING btree ("collection_id","collection_order");--> statement-breakpoint
CREATE INDEX "audio_stories_featured_order_idx" ON "audio_stories" USING btree ("featured","featured_order");--> statement-breakpoint
CREATE INDEX "audio_story_collections_status_order_idx" ON "audio_story_collections" USING btree ("status","display_order");--> statement-breakpoint
CREATE INDEX "audio_story_events_story_idx" ON "audio_story_events" USING btree ("story_id","created_at");--> statement-breakpoint
CREATE INDEX "audio_story_events_revision_idx" ON "audio_story_events" USING btree ("revision_id","created_at");--> statement-breakpoint
CREATE INDEX "audio_story_reviews_revision_idx" ON "audio_story_reviews" USING btree ("revision_id","created_at");--> statement-breakpoint
CREATE INDEX "audio_story_revisions_story_status_idx" ON "audio_story_revisions" USING btree ("story_id","status");--> statement-breakpoint
CREATE INDEX "audio_story_revisions_queue_idx" ON "audio_story_revisions" USING btree ("status","submitted_at");--> statement-breakpoint
CREATE INDEX "audio_story_revisions_publish_idx" ON "audio_story_revisions" USING btree ("status","publish_at");--> statement-breakpoint
CREATE INDEX "audio_story_revisions_reviewer_idx" ON "audio_story_revisions" USING btree ("assigned_reviewer_id","status");--> statement-breakpoint
CREATE UNIQUE INDEX "audio_story_tracks_revision_language_unique" ON "audio_story_tracks" USING btree ("revision_id","language");--> statement-breakpoint
CREATE UNIQUE INDEX "audio_story_transcripts_revision_language_unique" ON "audio_story_transcripts" USING btree ("revision_id","language");--> statement-breakpoint
ALTER TABLE "destinations" ADD CONSTRAINT "destinations_verified_by_users_id_fk" FOREIGN KEY ("verified_by") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "kenya_counties" ADD CONSTRAINT "kenya_counties_county_code_unique" UNIQUE("county_code");