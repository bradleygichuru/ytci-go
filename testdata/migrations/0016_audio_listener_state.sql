CREATE TABLE "audio_story_progress" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"user_id" text NOT NULL,
	"story_id" uuid NOT NULL,
	"position_seconds" integer DEFAULT 0 NOT NULL,
	"duration_seconds" integer DEFAULT 0 NOT NULL,
	"language" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);

CREATE TABLE "audio_story_saves" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"user_id" text NOT NULL,
	"story_id" uuid NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL
);

ALTER TABLE "audio_story_progress" ADD CONSTRAINT "audio_story_progress_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "audio_story_progress" ADD CONSTRAINT "audio_story_progress_story_id_audio_stories_id_fk" FOREIGN KEY ("story_id") REFERENCES "public"."audio_stories"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "audio_story_saves" ADD CONSTRAINT "audio_story_saves_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;
ALTER TABLE "audio_story_saves" ADD CONSTRAINT "audio_story_saves_story_id_audio_stories_id_fk" FOREIGN KEY ("story_id") REFERENCES "public"."audio_stories"("id") ON DELETE cascade ON UPDATE no action;
CREATE UNIQUE INDEX "audio_story_progress_user_story_unique" ON "audio_story_progress" USING btree ("user_id","story_id");
CREATE INDEX "audio_story_progress_user_idx" ON "audio_story_progress" USING btree ("user_id","updated_at");
CREATE UNIQUE INDEX "audio_story_saves_user_story_unique" ON "audio_story_saves" USING btree ("user_id","story_id");
CREATE INDEX "audio_story_saves_user_idx" ON "audio_story_saves" USING btree ("user_id","created_at");