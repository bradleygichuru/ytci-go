package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type DestinationsHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewDestinationsHandler(pool *pgxpool.Pool, r2client r2.Store) *DestinationsHandler {
	return &DestinationsHandler{pool: pool, r2: r2client}
}

func (h *DestinationsHandler) List(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	limit := int32(pr.Limit) + 1

	type adminDestination struct {
		ID                  pgtype.UUID      `json:"id"`
		Name                string           `json:"name"`
		Slug                string           `json:"slug"`
		County              string           `json:"county"`
		Locality            *string          `json:"locality"`
		Category            string           `json:"category"`
		Status              string           `json:"status"`
		MapLabel            *string          `json:"map_label"`
		AccessRoute         *string          `json:"access_route"`
		DistanceReference   *string          `json:"distance_reference"`
		ShortDescription    *string          `json:"short_description"`
		FullDescription     *string          `json:"full_description"`
		Significance        *string          `json:"significance"`
		History             *string          `json:"history"`
		ThingsToDo          *string          `json:"things_to_do"`
		SuitableAudiences   *string          `json:"suitable_audiences"`
		Duration            *string          `json:"duration"`
		Difficulty          *string          `json:"difficulty"`
		Seasonality         *string          `json:"seasonality"`
		IndicativeFees      *string          `json:"indicative_fees"`
		OpeningInfo         *string          `json:"opening_info"`
		TransportNotes      *string          `json:"transport_notes"`
		Accessibility       *string          `json:"accessibility"`
		Facilities          *string          `json:"facilities"`
		SafetyNotes         *string          `json:"safety_notes"`
		Source              *string          `json:"source"`
		ContentOwner        *string          `json:"content_owner"`
		VerificationStatus  *string          `json:"verification_status"`
		CreatedAt           pgtype.Timestamp `json:"created_at"`
		UpdatedAt           pgtype.Timestamp `json:"updated_at"`
		Media               json.RawMessage  `json:"media"`
	}

	q := `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
		d.status, d.map_label, d.access_route, d.distance_reference,
		d.short_description, d.full_description, d.significance, d.history,
		d.things_to_do, d.suitable_audiences, d.duration, d.difficulty,
		d.seasonality, d.indicative_fees, d.opening_info, d.transport_notes,
		d.accessibility, d.facilities, d.safety_notes, d.source,
		d.content_owner, d.verification_status, d.created_at, d.updated_at,
		COALESCE(
			(SELECT json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text,
				'caption', ma.caption,
				'credit', ma.credit
			) ORDER BY ma.display_order)
			FROM media_assets ma WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id),
			'[]'::json
		) AS media
		FROM destinations d`

	var rows pgx.Rows
	var err error

	if pr.Cursor != nil {
		var ts pgtype.Timestamp
		var uid pgtype.UUID
		if e := ts.Scan(pr.Cursor.SortValue); e != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
			return
		}
		if e := uid.Scan(pr.Cursor.ID); e != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
			return
		}
		rows, err = h.pool.Query(r.Context(),
			q+` WHERE (d.created_at, d.id) < ($1, $2) ORDER BY d.created_at DESC, d.id DESC LIMIT $3`,
			ts, uid, limit)
	} else {
		rows, err = h.pool.Query(r.Context(),
			q+` ORDER BY d.created_at DESC, d.id DESC LIMIT $1`, limit)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}
	defer rows.Close()

	var items []adminDestination
	for rows.Next() {
		var i adminDestination
		rows.Scan(&i.ID, &i.Name, &i.Slug, &i.County, &i.Locality, &i.Category,
			&i.Status, &i.MapLabel, &i.AccessRoute, &i.DistanceReference,
			&i.ShortDescription, &i.FullDescription, &i.Significance, &i.History,
			&i.ThingsToDo, &i.SuitableAudiences, &i.Duration, &i.Difficulty,
			&i.Seasonality, &i.IndicativeFees, &i.OpeningInfo, &i.TransportNotes,
			&i.Accessibility, &i.Facilities, &i.SafetyNotes, &i.Source,
			&i.ContentOwner, &i.VerificationStatus, &i.CreatedAt, &i.UpdatedAt, &i.Media)
		items = append(items, i)
	}

	hasMore := len(items) > int(limit-1)
	result := items
	if hasMore {
		result = items[:limit-1]
	}
	if result == nil {
		result = []adminDestination{}
	}

	resp := model.Paginated[adminDestination]{Items: result, HasMore: hasMore}
	if hasMore && len(result) > 0 {
		last := result[len(result)-1]
		ts := last.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		c := pagination.EncodeCursor(ts, pagination.UUIDString(last.ID.Bytes))
		resp.NextCursor = &c
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *DestinationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SLUG", "destination slug is required")
		return
	}

	type adminDetail struct {
		ID                  pgtype.UUID      `json:"id"`
		Name                string           `json:"name"`
		Slug                string           `json:"slug"`
		County              string           `json:"county"`
		Locality            *string          `json:"locality"`
		Category            string           `json:"category"`
		Status              string           `json:"status"`
		MapLabel            *string          `json:"map_label"`
		AccessRoute         *string          `json:"access_route"`
		DistanceReference   *string          `json:"distance_reference"`
		ShortDescription    *string          `json:"short_description"`
		FullDescription     *string          `json:"full_description"`
		Significance        *string          `json:"significance"`
		History             *string          `json:"history"`
		ThingsToDo          *string          `json:"things_to_do"`
		SuitableAudiences   *string          `json:"suitable_audiences"`
		Duration            *string          `json:"duration"`
		Difficulty          *string          `json:"difficulty"`
		Seasonality         *string          `json:"seasonality"`
		IndicativeFees      *string          `json:"indicative_fees"`
		OpeningInfo         *string          `json:"opening_info"`
		TransportNotes      *string          `json:"transport_notes"`
		Accessibility       *string          `json:"accessibility"`
		Facilities          *string          `json:"facilities"`
		SafetyNotes         *string          `json:"safety_notes"`
		Source              *string          `json:"source"`
		ContentOwner        *string          `json:"content_owner"`
		VerificationStatus  *string          `json:"verification_status"`
		CreatedAt           pgtype.Timestamp `json:"created_at"`
		UpdatedAt           pgtype.Timestamp `json:"updated_at"`
		Media               json.RawMessage  `json:"media"`
	}

	q := `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
		d.status, d.map_label, d.access_route, d.distance_reference,
		d.short_description, d.full_description, d.significance, d.history,
		d.things_to_do, d.suitable_audiences, d.duration, d.difficulty,
		d.seasonality, d.indicative_fees, d.opening_info, d.transport_notes,
		d.accessibility, d.facilities, d.safety_notes, d.source,
		d.content_owner, d.verification_status, d.created_at, d.updated_at,
		COALESCE(
			(SELECT json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text,
				'caption', ma.caption,
				'credit', ma.credit
			) ORDER BY ma.display_order)
			FROM media_assets ma WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id),
			'[]'::json
		) AS media
		FROM destinations d WHERE d.slug = $1`

	var dest adminDetail
	err := h.pool.QueryRow(r.Context(), q, slug).Scan(
		&dest.ID, &dest.Name, &dest.Slug, &dest.County, &dest.Locality, &dest.Category,
		&dest.Status, &dest.MapLabel, &dest.AccessRoute, &dest.DistanceReference,
		&dest.ShortDescription, &dest.FullDescription, &dest.Significance, &dest.History,
		&dest.ThingsToDo, &dest.SuitableAudiences, &dest.Duration, &dest.Difficulty,
		&dest.Seasonality, &dest.IndicativeFees, &dest.OpeningInfo, &dest.TransportNotes,
		&dest.Accessibility, &dest.Facilities, &dest.SafetyNotes, &dest.Source,
		&dest.ContentOwner, &dest.VerificationStatus, &dest.CreatedAt, &dest.UpdatedAt, &dest.Media)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "destination not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dest)
}

func (h *DestinationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string   `json:"name"`
		Slug               string   `json:"slug"`
		County             string   `json:"county"`
		Locality           string   `json:"locality,omitempty"`
		Category           string   `json:"category"`
		Lat                *float64 `json:"latitude,omitempty"`
		Lng                *float64 `json:"longitude,omitempty"`
		MapLabel           string   `json:"mapLabel,omitempty"`
		AccessRoute        string   `json:"accessRoute,omitempty"`
		DistanceReference  string   `json:"distanceReference,omitempty"`
		ShortDescription   string   `json:"shortDescription,omitempty"`
		FullDescription    string   `json:"fullDescription,omitempty"`
		Significance       string   `json:"significance,omitempty"`
		History            string   `json:"history,omitempty"`
		ThingsToDo         string   `json:"thingsToDo,omitempty"`
		SuitableAudiences  string   `json:"suitableAudiences,omitempty"`
		Duration           string   `json:"duration,omitempty"`
		Difficulty         string   `json:"difficulty,omitempty"`
		Seasonality        string   `json:"seasonality,omitempty"`
		IndicativeFees     string   `json:"indicativeFees,omitempty"`
		OpeningInfo        string   `json:"openingInfo,omitempty"`
		TransportNotes     string   `json:"transportNotes,omitempty"`
		Accessibility      string   `json:"accessibility,omitempty"`
		Facilities         string   `json:"facilities,omitempty"`
		SafetyNotes        string   `json:"safetyNotes,omitempty"`
		Source             string   `json:"source,omitempty"`
		ContentOwner       string   `json:"contentOwner,omitempty"`
		VerificationStatus string   `json:"verificationStatus,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	queries := gen.New(h.pool)
	dest, err := queries.CreateDestination(r.Context(), &gen.CreateDestinationParams{
		Name:               req.Name,
		Slug:               req.Slug,
		County:             req.County,
		Locality:           strPtr(req.Locality),
		Category:           req.Category,
		Status:             "draft",
		StMakepoint:        req.Lng,
		StMakepoint_2:      req.Lat,
		MapLabel:           strPtr(req.MapLabel),
		AccessRoute:        strPtr(req.AccessRoute),
		DistanceReference:  strPtr(req.DistanceReference),
		ShortDescription:   strPtr(req.ShortDescription),
		FullDescription:    strPtr(req.FullDescription),
		Significance:       strPtr(req.Significance),
		History:            strPtr(req.History),
		ThingsToDo:         strPtr(req.ThingsToDo),
		SuitableAudiences:  strPtr(req.SuitableAudiences),
		Duration:           strPtr(req.Duration),
		Difficulty:         strPtr(req.Difficulty),
		Seasonality:        strPtr(req.Seasonality),
		IndicativeFees:     strPtr(req.IndicativeFees),
		OpeningInfo:        strPtr(req.OpeningInfo),
		TransportNotes:     strPtr(req.TransportNotes),
		Accessibility:      strPtr(req.Accessibility),
		Facilities:         strPtr(req.Facilities),
		SafetyNotes:        strPtr(req.SafetyNotes),
		Source:             strPtr(req.Source),
		ContentOwner:       strPtr(req.ContentOwner),
		VerificationStatus: strPtr(req.VerificationStatus),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create destination")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dest)
}

func (h *DestinationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	var req struct {
		Name               *string  `json:"name,omitempty"`
		Slug               *string  `json:"slug,omitempty"`
		County             *string  `json:"county,omitempty"`
		Locality           *string  `json:"locality,omitempty"`
		Category           *string  `json:"category,omitempty"`
		Lat                *float64 `json:"latitude,omitempty"`
		Lng                *float64 `json:"longitude,omitempty"`
		MapLabel           *string  `json:"mapLabel,omitempty"`
		AccessRoute        *string  `json:"accessRoute,omitempty"`
		DistanceReference  *string  `json:"distanceReference,omitempty"`
		ShortDescription   *string  `json:"shortDescription,omitempty"`
		FullDescription    *string  `json:"fullDescription,omitempty"`
		Significance       *string  `json:"significance,omitempty"`
		History            *string  `json:"history,omitempty"`
		ThingsToDo         *string  `json:"thingsToDo,omitempty"`
		SuitableAudiences  *string  `json:"suitableAudiences,omitempty"`
		Duration           *string  `json:"duration,omitempty"`
		Difficulty         *string  `json:"difficulty,omitempty"`
		Seasonality        *string  `json:"seasonality,omitempty"`
		IndicativeFees     *string  `json:"indicativeFees,omitempty"`
		OpeningInfo        *string  `json:"openingInfo,omitempty"`
		TransportNotes     *string  `json:"transportNotes,omitempty"`
		Accessibility      *string  `json:"accessibility,omitempty"`
		Facilities         *string  `json:"facilities,omitempty"`
		SafetyNotes        *string  `json:"safetyNotes,omitempty"`
		Source             *string  `json:"source,omitempty"`
		ContentOwner       *string  `json:"contentOwner,omitempty"`
		VerificationStatus *string  `json:"verificationStatus,omitempty"`
		Status             *string  `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE destinations SET
		 name = CASE WHEN $2::text != '' THEN $2 ELSE name END,
		 slug = CASE WHEN $3::text != '' THEN $3 ELSE slug END,
		 county = CASE WHEN $4::text != '' THEN $4 ELSE county END,
		 locality = COALESCE($5::text, locality),
		 category = CASE WHEN $6::text != '' THEN $6 ELSE category END,
		 location = CASE WHEN $7::float8 IS NOT NULL AND $8::float8 IS NOT NULL THEN ST_SetSRID(ST_MakePoint($7::float8, $8::float8), 4326) ELSE location END,
		 map_label = COALESCE($9::text, map_label),
		 access_route = COALESCE($10::text, access_route),
		 distance_reference = COALESCE($11::text, distance_reference),
		 short_description = COALESCE($12::text, short_description),
		 full_description = COALESCE($13::text, full_description),
		 significance = COALESCE($14::text, significance),
		 history = COALESCE($15::text, history),
		 things_to_do = COALESCE($16::text, things_to_do),
		 suitable_audiences = COALESCE($17::text, suitable_audiences),
		 duration = COALESCE($18::text, duration),
		 difficulty = COALESCE($19::text, difficulty),
		 seasonality = COALESCE($20::text, seasonality),
		 indicative_fees = COALESCE($21::text, indicative_fees),
		 opening_info = COALESCE($22::text, opening_info),
		 transport_notes = COALESCE($23::text, transport_notes),
		 accessibility = COALESCE($24::text, accessibility),
		 facilities = COALESCE($25::text, facilities),
		 safety_notes = COALESCE($26::text, safety_notes),
		 source = COALESCE($27::text, source),
		 content_owner = COALESCE($28::text, content_owner),
		 verification_status = COALESCE($29::text, verification_status),
		 status = CASE WHEN $30::text != '' THEN $30 ELSE status END,
		 updated_at = now()
		 WHERE id = $1`,
		destID, valOrEmpty(req.Name), valOrEmpty(req.Slug),
		valOrEmpty(req.County), req.Locality, valOrEmpty(req.Category),
		req.Lng, req.Lat,
		req.MapLabel, req.AccessRoute, req.DistanceReference,
		req.ShortDescription, req.FullDescription,
		req.Significance, req.History, req.ThingsToDo, req.SuitableAudiences,
		req.Duration, req.Difficulty, req.Seasonality,
		req.IndicativeFees, req.OpeningInfo, req.TransportNotes,
		req.Accessibility, req.Facilities, req.SafetyNotes,
		req.Source, req.ContentOwner, req.VerificationStatus,
		valOrEmpty(req.Status))
	if err != nil {
		slog.Error("update destination", "error", err, "id", destID)
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update destination")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *DestinationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(),
		`UPDATE destinations SET status = 'archived', updated_at = now() WHERE id = $1`, destID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to archive destination")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "archived"})
}

func (h *DestinationsHandler) linkMedia(ctx context.Context, destID, mediaID string) error {
	tag, err := h.pool.Exec(ctx,
		`UPDATE media_assets SET entity_type = 'destination', entity_id = $1 WHERE id = $2`,
		destID, mediaID)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("media %q not found", mediaID)
	}
	return nil
}

func (h *DestinationsHandler) AddMedia(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	var req struct {
		HeroMediaID     string   `json:"heroMediaId,omitempty"`
		GalleryMediaIDs []string `json:"galleryMediaIds,omitempty"`
		VideoMediaID    string   `json:"videoMediaId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var fails []string
	if req.HeroMediaID != "" {
		if err := h.linkMedia(r.Context(), destID, req.HeroMediaID); err != nil {
			fails = append(fails, fmt.Sprintf("hero: %v", err))
		}
	}
	for _, gid := range req.GalleryMediaIDs {
		if err := h.linkMedia(r.Context(), destID, gid); err != nil {
			fails = append(fails, fmt.Sprintf("gallery %q: %v", gid, err))
		}
	}
	if req.VideoMediaID != "" {
		if err := h.linkMedia(r.Context(), destID, req.VideoMediaID); err != nil {
			fails = append(fails, fmt.Sprintf("video: %v", err))
		}
	}

	total := 0
	if req.HeroMediaID != "" {
		total++
	}
	total += len(req.GalleryMediaIDs)
	if req.VideoMediaID != "" {
		total++
	}

	status := "linked"
	httpStatus := http.StatusCreated
	if len(fails) > 0 {
		for _, f := range fails {
			slog.Warn("add media", "error", f)
		}
		if len(fails) >= total {
			status = "failed"
			httpStatus = http.StatusInternalServerError
		} else {
			status = "partial"
			httpStatus = http.StatusOK
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"errors": fails,
	})
}

func (h *DestinationsHandler) presignMedia(mediaJSON []byte, firstOnly bool) []byte {
	if h.r2 == nil || len(mediaJSON) == 0 {
		return mediaJSON
	}
	var items []map[string]any
	if err := json.Unmarshal(mediaJSON, &items); err != nil {
		return mediaJSON
	}
	limit := len(items)
	if firstOnly && limit > 1 {
		limit = 1
	}
	for _, item := range items[:limit] {
		ok, _ := item["objectKey"].(string)
		if ok != "" {
			if u, err := h.r2.PresignedGetURL(context.Background(), ok, 15*time.Minute); err == nil {
				item["url"] = u
			}
		}
		tk, _ := item["thumbnailKey"].(string)
		if tk != "" {
			if u, err := h.r2.PresignedGetURL(context.Background(), tk, 15*time.Minute); err == nil {
				item["thumbnailUrl"] = u
			}
		}
	}
	out, _ := json.Marshal(items)
	return out
}

func (h *DestinationsHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	county := r.URL.Query().Get("county")
	category := r.URL.Query().Get("category")
	search := r.URL.Query().Get("search")

	baseQ := `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
		d.short_description, d.full_description, d.significance, d.history,
		d.things_to_do, d.suitable_audiences, d.duration, d.difficulty,
		d.seasonality, d.indicative_fees, d.opening_info, d.transport_notes,
		d.accessibility, d.facilities, d.safety_notes, d.map_label,
		d.access_route, d.distance_reference,
		ST_X(d.location::geometry) AS lng, ST_Y(d.location::geometry) AS lat,
		COALESCE(
			(SELECT json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			) ORDER BY ma.display_order)
			FROM media_assets ma WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id),
			'[]'
		) AS media,
		d.created_at, d.updated_at
		FROM destinations d WHERE d.status = 'published'`

	if county != "" {
		baseQ += fmt.Sprintf(" AND d.county = '%s'", county)
	}
	if category != "" {
		baseQ += fmt.Sprintf(" AND d.category = '%s'", category)
	}
	if search != "" {
		baseQ += fmt.Sprintf(" AND (d.name ILIKE '%%%s%%' OR d.short_description ILIKE '%%%s%%')", search, search)
	}

	q := baseQ + " ORDER BY d.name LIMIT $1"

	rows, err := h.pool.Query(r.Context(), q, 50)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}
	defer rows.Close()

	type mobileMedia struct {
		ObjectKey    string `json:"objectKey"`
		ThumbnailKey string `json:"thumbnailKey,omitempty"`
		Type         string `json:"type,omitempty"`
		AltText      string `json:"altText,omitempty"`
		URL          string `json:"url,omitempty"`
		ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	}

	type mobileDestination struct {
		ID                string         `json:"id"`
		Name              string         `json:"name"`
		Slug              string         `json:"slug"`
		County            string         `json:"county"`
		Locality          *string        `json:"locality,omitempty"`
		Category          string         `json:"category"`
		ShortDescription  *string        `json:"shortDescription,omitempty"`
		FullDescription   *string        `json:"fullDescription,omitempty"`
		Significance      *string        `json:"significance,omitempty"`
		History           *string        `json:"history,omitempty"`
		ThingsToDo        *string        `json:"thingsToDo,omitempty"`
		SuitableAudiences *string        `json:"suitableAudiences,omitempty"`
		Duration          *string        `json:"duration,omitempty"`
		Difficulty        *string        `json:"difficulty,omitempty"`
		Seasonality       *string        `json:"seasonality,omitempty"`
		IndicativeFees    *string        `json:"indicativeFees,omitempty"`
		OpeningInfo       *string        `json:"openingInfo,omitempty"`
		TransportNotes    *string        `json:"transportNotes,omitempty"`
		Accessibility     *string        `json:"accessibility,omitempty"`
		Facilities        *string        `json:"facilities,omitempty"`
		SafetyNotes       *string        `json:"safetyNotes,omitempty"`
		MapLabel          *string        `json:"mapLabel,omitempty"`
		AccessRoute       *string        `json:"accessRoute,omitempty"`
		DistanceReference *string        `json:"distanceReference,omitempty"`
		Media             []mobileMedia  `json:"media"`
		Lng               interface{}    `json:"lng"`
		Lat               interface{}    `json:"lat"`
		CreatedAt         pgtype.Timestamp `json:"createdAt"`
		UpdatedAt         pgtype.Timestamp `json:"updatedAt"`
	}

	var items []mobileDestination
	for rows.Next() {
		var i mobileDestination
		var mediaJSON []byte
		rows.Scan(&i.ID, &i.Name, &i.Slug, &i.County, &i.Locality, &i.Category,
			&i.ShortDescription, &i.FullDescription, &i.Significance, &i.History,
			&i.ThingsToDo, &i.SuitableAudiences, &i.Duration, &i.Difficulty,
			&i.Seasonality, &i.IndicativeFees, &i.OpeningInfo, &i.TransportNotes,
			&i.Accessibility, &i.Facilities, &i.SafetyNotes, &i.MapLabel,
			&i.AccessRoute, &i.DistanceReference,
			&i.Lng, &i.Lat, &mediaJSON, &i.CreatedAt, &i.UpdatedAt)
		if mediaJSON != nil {
			mediaJSON = h.presignMedia(mediaJSON, true)
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal destination media", "dest_id", i.ID, "error", err)
			}
		}
		if i.Media == nil {
			i.Media = []mobileMedia{}
		}
		items = append(items, i)
	}
	if items == nil {
		items = []mobileDestination{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (h *DestinationsHandler) GetMobile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SLUG", "destination slug is required")
		return
	}

	q := `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
		d.short_description, d.full_description, d.significance, d.history,
		d.things_to_do, d.suitable_audiences, d.duration, d.difficulty,
		d.seasonality, d.indicative_fees, d.opening_info, d.transport_notes,
		d.accessibility, d.facilities, d.safety_notes, d.map_label,
		d.access_route, d.distance_reference,
		ST_X(d.location::geometry) AS lng, ST_Y(d.location::geometry) AS lat,
		COALESCE(
			(SELECT json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			) ORDER BY ma.display_order)
			FROM media_assets ma WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id),
			'[]'
		) AS media,
		d.created_at, d.updated_at
		FROM destinations d WHERE d.slug = $1`

	type mobileMedia struct {
		ObjectKey    string `json:"objectKey"`
		ThumbnailKey string `json:"thumbnailKey,omitempty"`
		Type         string `json:"type,omitempty"`
		AltText      string `json:"altText,omitempty"`
		URL          string `json:"url,omitempty"`
		ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	}

	type mobileDestination struct {
		ID                string         `json:"id"`
		Name              string         `json:"name"`
		Slug              string         `json:"slug"`
		County            string         `json:"county"`
		Locality          *string        `json:"locality,omitempty"`
		Category          string         `json:"category"`
		ShortDescription  *string        `json:"shortDescription,omitempty"`
		FullDescription   *string        `json:"fullDescription,omitempty"`
		Significance      *string        `json:"significance,omitempty"`
		History           *string        `json:"history,omitempty"`
		ThingsToDo        *string        `json:"thingsToDo,omitempty"`
		SuitableAudiences *string        `json:"suitableAudiences,omitempty"`
		Duration          *string        `json:"duration,omitempty"`
		Difficulty        *string        `json:"difficulty,omitempty"`
		Seasonality       *string        `json:"seasonality,omitempty"`
		IndicativeFees    *string        `json:"indicativeFees,omitempty"`
		OpeningInfo       *string        `json:"openingInfo,omitempty"`
		TransportNotes    *string        `json:"transportNotes,omitempty"`
		Accessibility     *string        `json:"accessibility,omitempty"`
		Facilities        *string        `json:"facilities,omitempty"`
		SafetyNotes       *string        `json:"safetyNotes,omitempty"`
		MapLabel          *string        `json:"mapLabel,omitempty"`
		AccessRoute       *string        `json:"accessRoute,omitempty"`
		DistanceReference *string        `json:"distanceReference,omitempty"`
		Media             []mobileMedia  `json:"media"`
		Lng               interface{}    `json:"lng"`
		Lat               interface{}    `json:"lat"`
		CreatedAt         pgtype.Timestamp `json:"createdAt"`
		UpdatedAt         pgtype.Timestamp `json:"updatedAt"`
	}

	var dest mobileDestination
	var mediaJSON []byte
	err := h.pool.QueryRow(r.Context(), q, slug).Scan(&dest.ID, &dest.Name, &dest.Slug, &dest.County, &dest.Locality, &dest.Category,
		&dest.ShortDescription, &dest.FullDescription, &dest.Significance, &dest.History,
		&dest.ThingsToDo, &dest.SuitableAudiences, &dest.Duration, &dest.Difficulty,
		&dest.Seasonality, &dest.IndicativeFees, &dest.OpeningInfo, &dest.TransportNotes,
		&dest.Accessibility, &dest.Facilities, &dest.SafetyNotes, &dest.MapLabel,
		&dest.AccessRoute, &dest.DistanceReference,
		&dest.Lng, &dest.Lat, &mediaJSON, &dest.CreatedAt, &dest.UpdatedAt)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "destination not found")
		return
	}

	if mediaJSON != nil {
		mediaJSON = h.presignMedia(mediaJSON, false)
		if err := json.Unmarshal(mediaJSON, &dest.Media); err != nil {
			slog.Warn("failed to unmarshal destination detail media", "dest_id", dest.ID, "error", err)
		}
	}
	if dest.Media == nil {
		dest.Media = []mobileMedia{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dest)
}	

func (h *DestinationsHandler) NearbyMobile(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	radiusStr := r.URL.Query().Get("radius_km")

	lat, _ := strconv.ParseFloat(latStr, 64)
	lng, _ := strconv.ParseFloat(lngStr, 64)
	radiusMeters := 50.0 * 1000
	if r, err := strconv.ParseFloat(radiusStr, 64); err == nil && r > 0 {
		radiusMeters = r * 1000
	}

	q := `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
		d.short_description, d.full_description, d.significance, d.history,
		d.things_to_do, d.suitable_audiences, d.duration, d.difficulty,
		d.seasonality, d.indicative_fees, d.opening_info, d.transport_notes,
		d.accessibility, d.facilities, d.safety_notes, d.map_label,
		d.access_route, d.distance_reference,
		ST_X(d.location::geometry) AS lng, ST_Y(d.location::geometry) AS lat,
		COALESCE(
			(SELECT json_agg(json_build_object(
				'objectKey', ma.object_key,
				'thumbnailKey', ma.thumbnail_key,
				'type', ma.type,
				'altText', ma.alt_text
			) ORDER BY ma.display_order)
			FROM media_assets ma WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id),
			'[]'
		) AS media,
		ST_Distance(d.location, ST_MakePoint($1, $2)::geography) AS distance_meters,
		d.created_at, d.updated_at
		FROM destinations d
		WHERE d.status = 'published'
		AND ST_DWithin(d.location, ST_MakePoint($1, $2)::geography, $3)
		ORDER BY distance_meters ASC
		LIMIT $4`

	type nearbyMobileMedia struct {
		ObjectKey    string `json:"objectKey"`
		ThumbnailKey string `json:"thumbnailKey,omitempty"`
		Type         string `json:"type,omitempty"`
		AltText      string `json:"altText,omitempty"`
		URL          string `json:"url,omitempty"`
		ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	}

	type nearbyMobileDestination struct {
		ID                string            `json:"id"`
		Name              string            `json:"name"`
		Slug              string            `json:"slug"`
		County            string            `json:"county"`
		Locality          *string           `json:"locality,omitempty"`
		Category          string            `json:"category"`
		ShortDescription  *string           `json:"shortDescription,omitempty"`
		FullDescription   *string           `json:"fullDescription,omitempty"`
		Significance      *string           `json:"significance,omitempty"`
		History           *string           `json:"history,omitempty"`
		ThingsToDo        *string           `json:"thingsToDo,omitempty"`
		SuitableAudiences *string           `json:"suitableAudiences,omitempty"`
		Duration          *string           `json:"duration,omitempty"`
		Difficulty        *string           `json:"difficulty,omitempty"`
		Seasonality       *string           `json:"seasonality,omitempty"`
		IndicativeFees    *string           `json:"indicativeFees,omitempty"`
		OpeningInfo       *string           `json:"openingInfo,omitempty"`
		TransportNotes    *string           `json:"transportNotes,omitempty"`
		Accessibility     *string           `json:"accessibility,omitempty"`
		Facilities        *string           `json:"facilities,omitempty"`
		SafetyNotes       *string           `json:"safetyNotes,omitempty"`
		MapLabel          *string           `json:"mapLabel,omitempty"`
		AccessRoute       *string           `json:"accessRoute,omitempty"`
		DistanceReference *string           `json:"distanceReference,omitempty"`
		Media             []nearbyMobileMedia `json:"media"`
		DistanceMeters    float64           `json:"distanceMeters"`
		Lng               interface{}       `json:"lng"`
		Lat               interface{}       `json:"lat"`
		CreatedAt         pgtype.Timestamp  `json:"createdAt"`
		UpdatedAt         pgtype.Timestamp  `json:"updatedAt"`
	}

	rows, err := h.pool.Query(r.Context(), q, lng, lat, radiusMeters, 20)
	if err != nil {
		slog.Warn("nearby query failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]json.RawMessage{})
		return
	}
	defer rows.Close()

	var items []nearbyMobileDestination
	for rows.Next() {
		var i nearbyMobileDestination
		var mediaJSON []byte
		rows.Scan(&i.ID, &i.Name, &i.Slug, &i.County, &i.Locality, &i.Category,
			&i.ShortDescription, &i.FullDescription, &i.Significance, &i.History,
			&i.ThingsToDo, &i.SuitableAudiences, &i.Duration, &i.Difficulty,
			&i.Seasonality, &i.IndicativeFees, &i.OpeningInfo, &i.TransportNotes,
			&i.Accessibility, &i.Facilities, &i.SafetyNotes, &i.MapLabel,
			&i.AccessRoute, &i.DistanceReference,
			&i.Lng, &i.Lat, &mediaJSON, &i.DistanceMeters, &i.CreatedAt, &i.UpdatedAt)
		if mediaJSON != nil {
			mediaJSON = h.presignMedia(mediaJSON, true)
			if err := json.Unmarshal(mediaJSON, &i.Media); err != nil {
				slog.Warn("failed to unmarshal nearby media", "dest_id", i.ID, "error", err)
			}
		}
		if i.Media == nil {
			i.Media = []nearbyMobileMedia{}
		}
		items = append(items, i)
	}
	if items == nil {
		items = []nearbyMobileDestination{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (h *DestinationsHandler) Nearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	radiusStr := r.URL.Query().Get("radius_km")

	lat, _ := strconv.ParseFloat(latStr, 64)
	lng, _ := strconv.ParseFloat(lngStr, 64)
	radiusMeters := 50.0 * 1000
	if r, err := strconv.ParseFloat(radiusStr, 64); err == nil && r > 0 {
		radiusMeters = r * 1000
	}

	queries := gen.New(h.pool)
	results, err := queries.FindNearbyDestinations(r.Context(), &gen.FindNearbyDestinationsParams{
		StMakepoint:   lng,
		StMakepoint_2: lat,
		StDwithin:     radiusMeters,
		Limit:         20,
	})
	if err != nil {
		slog.Warn("nearby query failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]json.RawMessage{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
