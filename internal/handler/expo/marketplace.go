package expo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

// MarketplaceHandler serves the public Marketplace, Stays, and featured eSIM
// surfaces plus privacy-safe outbound redirect tracking.
type MarketplaceHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewMarketplaceHandler(pool *pgxpool.Pool, r2client r2.Store) *MarketplaceHandler {
	return &MarketplaceHandler{pool: pool, r2: r2client}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidPattern.MatchString(s) }

func (h *MarketplaceHandler) presign(ctx context.Context, key *string) *string {
	if h.r2 == nil || key == nil || *key == "" {
		return nil
	}
	u, err := h.r2.PresignedGetURL(ctx, *key, 15*time.Minute)
	if err != nil {
		slog.Warn("presign marketplace image", "key", *key, "error", err)
		return nil
	}
	return &u
}

type marketplaceBusinessSummary struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	Category         string  `json:"category"`
	Tagline          *string `json:"tagline"`
	ShortDescription string  `json:"shortDescription"`
	County           *string `json:"county"`
	Locality         *string `json:"locality"`
	ServiceArea      *string `json:"serviceArea"`
	ImageURL         *string `json:"imageUrl"`
	ImageAlt         *string `json:"imageAlt"`
	IsFeatured       bool    `json:"isFeatured"`
	DisplayOrder     int     `json:"displayOrder"`
}

func (b marketplaceBusinessSummary) CursorValues() (string, string) {
	return strconv.Itoa(b.DisplayOrder), b.ID
}

type marketplaceBusiness struct {
	marketplaceBusinessSummary
	FullDescription *string  `json:"fullDescription"`
	Address         *string  `json:"address"`
	LocationLat     *float64 `json:"locationLat"`
	LocationLng     *float64 `json:"locationLng"`
	ContactEmail    *string  `json:"contactEmail"`
	ContactPhone    *string  `json:"contactPhone"`
	RedirectURL     string   `json:"redirectUrl"`
	RedirectLabel   *string  `json:"redirectLabel"`
	Disclosure      *string  `json:"disclosure"`
}

type accommodationSummary struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	Tier             string  `json:"tier"`
	PropertyType     string  `json:"propertyType"`
	ShortDescription string  `json:"shortDescription"`
	County           string  `json:"county"`
	Locality         *string `json:"locality"`
	PriceFromKES     *int    `json:"priceFromKes"`
	PriceNote        *string `json:"priceNote"`
	PriceCheckedAt   *string `json:"priceCheckedAt"`
	DistanceNote     *string `json:"distanceNote"`
	CancellationNote *string `json:"cancellationNote"`
	PaymentNote      *string `json:"paymentNote"`
	IncludedNote     *string `json:"includedNote"`
	BadgeLabel       *string `json:"badgeLabel"`
	ImageURL         *string `json:"imageUrl"`
	ImageAlt         *string `json:"imageAlt"`
	IsFeatured       bool    `json:"isFeatured"`
	DisplayOrder     int     `json:"displayOrder"`
}

func (a accommodationSummary) CursorValues() (string, string) {
	return strconv.Itoa(a.DisplayOrder), a.ID
}

type accommodationDetail struct {
	accommodationSummary
	FullDescription *string  `json:"fullDescription"`
	Address         *string  `json:"address"`
	LocationLat     *float64 `json:"locationLat"`
	LocationLng     *float64 `json:"locationLng"`
	Amenities       []string `json:"amenities"`
	Highlights      []string `json:"highlights"`
	BookingURL      *string  `json:"bookingUrl"`
	DirectURL       *string  `json:"directUrl"`
	Disclosure      *string  `json:"disclosure"`
	ContactEmail    *string  `json:"contactEmail"`
	ContactPhone    *string  `json:"contactPhone"`
}

type esimProvider struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	Headline      *string `json:"headline"`
	Description   string  `json:"description"`
	CoverageNote  *string `json:"coverageNote"`
	PriceLabel    *string `json:"priceLabel"`
	RedirectURL   string  `json:"redirectUrl"`
	RedirectLabel *string `json:"redirectLabel"`
	Disclosure    *string `json:"disclosure"`
	LogoURL       *string `json:"logoUrl"`
	LogoAlt       *string `json:"logoAlt"`
}

func (h *MarketplaceHandler) ListBusinesses(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	args := []any{}
	where := []string{"status = 'published'"}

	if category := strings.TrimSpace(r.URL.Query().Get("category")); category != "" {
		where = append(where, fmt.Sprintf("category = $%d", len(args)+1))
		args = append(args, category)
	}
	if county := strings.TrimSpace(r.URL.Query().Get("county")); county != "" {
		where = append(where, fmt.Sprintf("county = $%d", len(args)+1))
		args = append(args, county)
	}
	if r.URL.Query().Get("featured") == "true" {
		where = append(where, "is_featured = true")
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		n := len(args) + 1
		where = append(where, fmt.Sprintf(
			"(name ILIKE $%d OR COALESCE(tagline, '') ILIKE $%d OR short_description ILIKE $%d OR COALESCE(locality, '') ILIKE $%d OR COALESCE(county, '') ILIKE $%d)",
			n, n, n, n, n,
		))
		args = append(args, "%"+q+"%")
	}

	if pr.Cursor != nil {
		order, err := strconv.Atoi(pr.Cursor.SortValue)
		if err == nil && isUUID(pr.Cursor.ID) {
			n := len(args) + 1
			where = append(where, fmt.Sprintf("(display_order, id) > ($%d, $%d::uuid)", n, n+1))
			args = append(args, order, pr.Cursor.ID)
		}
	}

	query := `SELECT id::text, name, slug, category, tagline, short_description, county, locality,
	                 service_area, image_url, image_alt, is_featured, display_order
	          FROM partner_businesses
	          WHERE ` + strings.Join(where, " AND ") + `
	          ORDER BY display_order ASC, id ASC
	          LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, pr.Limit+1)

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list marketplace businesses", err)
		return
	}
	defer rows.Close()

	items := make([]marketplaceBusinessSummary, 0, pr.Limit)
	for rows.Next() {
		var b marketplaceBusinessSummary
		var imageKey *string
		if err := rows.Scan(&b.ID, &b.Name, &b.Slug, &b.Category, &b.Tagline, &b.ShortDescription,
			&b.County, &b.Locality, &b.ServiceArea, &imageKey, &b.ImageAlt, &b.IsFeatured, &b.DisplayOrder); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan marketplace business", err)
			return
		}
		b.ImageURL = h.presign(r.Context(), imageKey)
		items = append(items, b)
	}

	writePage(w, items, pr.Limit, marketplaceBusinessSummary.CursorValues)
}

func (h *MarketplaceHandler) GetBusiness(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var b marketplaceBusiness
	var imageKey *string
	err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, name, slug, category, tagline, short_description, county, locality,
		       service_area, image_url, image_alt, is_featured, display_order,
		       full_description, address, location_lat, location_lng, contact_email, contact_phone,
		       redirect_url, redirect_label, disclosure
		FROM partner_businesses
		WHERE slug = $1 AND status = 'published'`, slug).Scan(
		&b.ID, &b.Name, &b.Slug, &b.Category, &b.Tagline, &b.ShortDescription, &b.County, &b.Locality,
		&b.ServiceArea, &imageKey, &b.ImageAlt, &b.IsFeatured, &b.DisplayOrder,
		&b.FullDescription, &b.Address, &b.LocationLat, &b.LocationLng, &b.ContactEmail, &b.ContactPhone,
		&b.RedirectURL, &b.RedirectLabel, &b.Disclosure,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "business not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch marketplace business", err)
		return
	}
	b.ImageURL = h.presign(r.Context(), imageKey)
	writeJSON(w, http.StatusOK, b)
}

func (h *MarketplaceHandler) ListStays(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	args := []any{}
	where := []string{"status = 'published'"}

	if tier := strings.TrimSpace(r.URL.Query().Get("tier")); tier != "" {
		where = append(where, fmt.Sprintf("tier = $%d", len(args)+1))
		args = append(args, tier)
	}
	if county := strings.TrimSpace(r.URL.Query().Get("county")); county != "" {
		where = append(where, fmt.Sprintf("county = $%d", len(args)+1))
		args = append(args, county)
	}
	if r.URL.Query().Get("featured") == "true" {
		where = append(where, "is_featured = true")
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		n := len(args) + 1
		where = append(where, fmt.Sprintf(
			"(name ILIKE $%d OR short_description ILIKE $%d OR COALESCE(locality, '') ILIKE $%d OR county ILIKE $%d)",
			n, n, n, n,
		))
		args = append(args, "%"+q+"%")
	}

	if pr.Cursor != nil {
		order, err := strconv.Atoi(pr.Cursor.SortValue)
		if err == nil && isUUID(pr.Cursor.ID) {
			n := len(args) + 1
			where = append(where, fmt.Sprintf("(display_order, id) > ($%d, $%d::uuid)", n, n+1))
			args = append(args, order, pr.Cursor.ID)
		}
	}

	query := `SELECT id::text, name, slug, tier, property_type, short_description, county,
	                 locality, price_from_kes, price_note, price_checked_at::text, distance_note,
	                 cancellation_note, payment_note, included_note, badge_label,
	                 image_url, image_alt, is_featured, display_order
	          FROM accommodation_properties
	          WHERE ` + strings.Join(where, " AND ") + `
	          ORDER BY display_order ASC, id ASC
	          LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, pr.Limit+1)

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list accommodations", err)
		return
	}
	defer rows.Close()

	items := make([]accommodationSummary, 0, pr.Limit)
	for rows.Next() {
		var a accommodationSummary
		var imageKey *string
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.Tier, &a.PropertyType, &a.ShortDescription,
			&a.County, &a.Locality, &a.PriceFromKES, &a.PriceNote, &a.PriceCheckedAt, &a.DistanceNote,
			&a.CancellationNote, &a.PaymentNote, &a.IncludedNote, &a.BadgeLabel,
			&imageKey, &a.ImageAlt, &a.IsFeatured, &a.DisplayOrder); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan accommodation", err)
			return
		}
		a.ImageURL = h.presign(r.Context(), imageKey)
		items = append(items, a)
	}

	writePage(w, items, pr.Limit, accommodationSummary.CursorValues)
}

func (h *MarketplaceHandler) GetStay(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var a accommodationDetail
	var imageKey *string
	err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, name, slug, tier, property_type, short_description, county, locality,
		       price_from_kes, price_note, price_checked_at::text, distance_note, cancellation_note,
		       payment_note, included_note, badge_label, image_url, image_alt, is_featured, display_order,
		       full_description, address, location_lat, location_lng, amenities, highlights,
		       booking_url, direct_url, disclosure, contact_email, contact_phone
		FROM accommodation_properties
		WHERE slug = $1 AND status = 'published'`, slug).Scan(
		&a.ID, &a.Name, &a.Slug, &a.Tier, &a.PropertyType, &a.ShortDescription, &a.County, &a.Locality,
		&a.PriceFromKES, &a.PriceNote, &a.PriceCheckedAt, &a.DistanceNote, &a.CancellationNote,
		&a.PaymentNote, &a.IncludedNote, &a.BadgeLabel, &imageKey, &a.ImageAlt, &a.IsFeatured, &a.DisplayOrder,
		&a.FullDescription, &a.Address, &a.LocationLat, &a.LocationLng, &a.Amenities, &a.Highlights,
		&a.BookingURL, &a.DirectURL, &a.Disclosure, &a.ContactEmail, &a.ContactPhone,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "accommodation not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch accommodation", err)
		return
	}
	if a.Amenities == nil {
		a.Amenities = []string{}
	}
	if a.Highlights == nil {
		a.Highlights = []string{}
	}
	a.ImageURL = h.presign(r.Context(), imageKey)
	writeJSON(w, http.StatusOK, a)
}

func (h *MarketplaceHandler) GetFeaturedEsim(w http.ResponseWriter, r *http.Request) {
	var p esimProvider
	var logoKey *string
	err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, name, slug, headline, description, coverage_note, price_label,
		       redirect_url, redirect_label, disclosure, logo_url, logo_alt
		FROM esim_providers
		WHERE status = 'published' AND is_featured = true
		ORDER BY display_order ASC, id ASC
		LIMIT 1`).Scan(
		&p.ID, &p.Name, &p.Slug, &p.Headline, &p.Description, &p.CoverageNote, &p.PriceLabel,
		&p.RedirectURL, &p.RedirectLabel, &p.Disclosure, &logoKey, &p.LogoAlt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]any{"provider": nil})
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch featured eSIM provider", err)
		return
	}
	p.LogoURL = h.presign(r.Context(), logoKey)
	writeJSON(w, http.StatusOK, map[string]any{"provider": p})
}

type redirectRequest struct {
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId"`
	Source     string `json:"source"`
	Platform   string `json:"platform"`
}

func (h *MarketplaceHandler) RecordRedirect(w http.ResponseWriter, r *http.Request) {
	var req redirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	req.EntityType = strings.TrimSpace(req.EntityType)
	req.EntityID = strings.TrimSpace(req.EntityID)
	req.Source = strings.TrimSpace(req.Source)
	req.Platform = strings.TrimSpace(req.Platform)

	var table string
	switch req.EntityType {
	case "partner_business":
		table = "partner_businesses"
	case "accommodation":
		table = "accommodation_properties"
	case "esim_provider":
		table = "esim_providers"
	default:
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid entityType")
		return
	}
	if !isUUID(req.EntityID) {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid entityId")
		return
	}
	if req.Platform != "ios" && req.Platform != "android" && req.Platform != "web" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid platform")
		return
	}
	if len(req.Source) > 64 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "source is too long")
		return
	}

	var exists bool
	err := h.pool.QueryRow(r.Context(), fmt.Sprintf(
		`SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1::uuid AND status = 'published')`, table,
	), req.EntityID).Scan(&exists)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to verify redirect target", err)
		return
	}
	if !exists {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "redirect target not found")
		return
	}

	var userID any
	if uid := middleware.UserID(r.Context()); uid != "" {
		userID = uid
	}

	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO marketplace_redirects (entity_type, entity_id, user_id, source, platform)
		VALUES ($1, $2::uuid, $3, $4, $5)`,
		req.EntityType, req.EntityID, userID, req.Source, req.Platform,
	)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record redirect", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writePage[T any](w http.ResponseWriter, items []T, limit int, cursor func(T) (string, string)) {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	resp := model.Paginated[T]{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		sortValue, id := cursor(items[len(items)-1])
		c := pagination.EncodeCursor(sortValue, id)
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}
