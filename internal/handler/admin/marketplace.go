package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

// MarketplaceAdminHandler owns admin CRUD for partner businesses,
// accommodation properties, and the featured eSIM provider.
type MarketplaceAdminHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewMarketplaceAdminHandler(pool *pgxpool.Pool, r2client r2.Store) *MarketplaceAdminHandler {
	return &MarketplaceAdminHandler{pool: pool, r2: r2client}
}

// optional distinguishes an omitted JSON field from an explicit null.
// This lets PATCH preserve omitted values while still allowing a client to
// clear a nullable numeric field (price, latitude, longitude).
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9-]{2,80}$`)
	uuidPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	validCategory = map[string]bool{
		"experiences": true, "food_drink": true, "transport": true,
		"shopping": true, "services": true, "connectivity": true,
	}
	validTier         = map[string]bool{"luxury": true, "midrange": true, "budget": true}
	validPropertyType = map[string]bool{
		"lodge": true, "camp": true, "hotel": true, "hostel": true,
		"guesthouse": true, "apartment": true, "villa": true,
	}
	validStatus = map[string]bool{"draft": true, "published": true, "archived": true}
)

func (h *MarketplaceAdminHandler) presign(ctx context.Context, key *string) *string {
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

func isUUID(s string) bool { return uuidPattern.MatchString(s) }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func defaultIfEmpty(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func derefString(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return strings.TrimSpace(*p)
}

func derefNullableString(p *string, fallback *string) *string {
	if p == nil {
		return fallback
	}
	return nullIfEmpty(*p)
}

func derefBool(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func derefInt(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validOptionalEmail(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	_, err := mail.ParseAddress(s)
	return err == nil
}

// ── Partner businesses ──

type businessRequest struct {
	Name             *string           `json:"name"`
	Slug             *string           `json:"slug"`
	Category         *string           `json:"category"`
	Tagline          *string           `json:"tagline"`
	ShortDescription *string           `json:"shortDescription"`
	FullDescription  *string           `json:"fullDescription"`
	County           *string           `json:"county"`
	Locality         *string           `json:"locality"`
	ServiceArea      *string           `json:"serviceArea"`
	Address          *string           `json:"address"`
	LocationLat      optional[float64] `json:"locationLat"`
	LocationLng      optional[float64] `json:"locationLng"`
	ContactEmail     *string           `json:"contactEmail"`
	ContactPhone     *string           `json:"contactPhone"`
	RedirectURL      *string           `json:"redirectUrl"`
	RedirectLabel    *string           `json:"redirectLabel"`
	Disclosure       *string           `json:"disclosure"`
	ImageURL         *string           `json:"imageUrl"`
	ImageAlt         *string           `json:"imageAlt"`
	ImageCredit      *string           `json:"imageCredit"`
	Status           *string           `json:"status"`
	IsFeatured       *bool             `json:"isFeatured"`
	DisplayOrder     *int              `json:"displayOrder"`
}

type adminBusiness struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Slug             string   `json:"slug"`
	Category         string   `json:"category"`
	Tagline          *string  `json:"tagline"`
	ShortDescription string   `json:"shortDescription"`
	FullDescription  *string  `json:"fullDescription"`
	County           *string  `json:"county"`
	Locality         *string  `json:"locality"`
	ServiceArea      *string  `json:"serviceArea"`
	Address          *string  `json:"address"`
	LocationLat      *float64 `json:"locationLat"`
	LocationLng      *float64 `json:"locationLng"`
	ContactEmail     *string  `json:"contactEmail"`
	ContactPhone     *string  `json:"contactPhone"`
	RedirectURL      string   `json:"redirectUrl"`
	RedirectLabel    *string  `json:"redirectLabel"`
	Disclosure       *string  `json:"disclosure"`
	ImageURL         *string  `json:"imageUrl"`
	ImageURLKey      *string  `json:"imageUrlKey"`
	ImageAlt         *string  `json:"imageAlt"`
	ImageCredit      *string  `json:"imageCredit"`
	Status           string   `json:"status"`
	IsFeatured       bool     `json:"isFeatured"`
	DisplayOrder     int      `json:"displayOrder"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

func (b adminBusiness) CursorValues() (string, string) {
	return strconv.Itoa(b.DisplayOrder), b.ID
}

func scanBusiness(row pgx.Row, b *adminBusiness) error {
	var imageKey *string
	err := row.Scan(&b.ID, &b.Name, &b.Slug, &b.Category, &b.Tagline, &b.ShortDescription,
		&b.FullDescription, &b.County, &b.Locality, &b.ServiceArea, &b.Address,
		&b.LocationLat, &b.LocationLng, &b.ContactEmail, &b.ContactPhone,
		&b.RedirectURL, &b.RedirectLabel, &b.Disclosure, &imageKey, &b.ImageAlt, &b.ImageCredit,
		&b.Status, &b.IsFeatured, &b.DisplayOrder, &b.CreatedAt, &b.UpdatedAt)
	b.ImageURLKey = imageKey
	return err
}

const businessColumns = `id::text, name, slug, category, tagline, short_description, full_description,
	county, locality, service_area, address, location_lat, location_lng, contact_email, contact_phone,
	redirect_url, redirect_label, disclosure, image_url, image_alt, image_credit, status, is_featured,
	display_order, created_at::text, updated_at::text`

func (h *MarketplaceAdminHandler) ListBusinesses(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	args := []any{}
	where := []string{"1 = 1"}

	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		where = append(where, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, status)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		n := len(args) + 1
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR slug ILIKE $%d OR COALESCE(county, '') ILIKE $%d)", n, n, n))
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

	query := `SELECT ` + businessColumns + `
	          FROM partner_businesses
	          WHERE ` + strings.Join(where, " AND ") + `
	          ORDER BY display_order ASC, id ASC
	          LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, pr.Limit+1)

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list businesses", err)
		return
	}
	defer rows.Close()

	items := make([]adminBusiness, 0, pr.Limit)
	for rows.Next() {
		var b adminBusiness
		if err := scanBusiness(rows, &b); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan business", err)
			return
		}
		b.ImageURL = h.presign(r.Context(), b.ImageURLKey)
		items = append(items, b)
	}

	writeAdminPage(w, items, pr.Limit, func(b adminBusiness) (string, string) { return b.CursorValues() })
}

func (h *MarketplaceAdminHandler) GetBusiness(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid business id")
		return
	}
	var b adminBusiness
	err := scanBusiness(h.pool.QueryRow(r.Context(), `SELECT `+businessColumns+` FROM partner_businesses WHERE id = $1`, id), &b)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "business not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch business", err)
		return
	}
	b.ImageURL = h.presign(r.Context(), b.ImageURLKey)
	writeAdminJSON(w, http.StatusOK, b)
}

func (h *MarketplaceAdminHandler) CreateBusiness(w http.ResponseWriter, r *http.Request) {
	var req businessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, "")
	slug := derefString(req.Slug, "")
	category := derefString(req.Category, "")
	shortDescription := derefString(req.ShortDescription, "")
	redirectURL := derefString(req.RedirectURL, "")
	status := derefString(req.Status, "draft")
	displayOrder := derefInt(req.DisplayOrder, 0)

	if err := validateBusiness(name, slug, category, shortDescription, redirectURL, status, displayOrder); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if !validOptionalEmail(derefString(req.ContactEmail, "")) {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "contactEmail is not a valid email address")
		return
	}

	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO partner_businesses (
			name, slug, category, tagline, short_description, full_description, county, locality,
			service_area, address, location_lat, location_lng, contact_email, contact_phone,
			redirect_url, redirect_label, disclosure, image_url, image_alt, image_credit,
			status, is_featured, display_order, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		RETURNING id::text`,
		name, slug, category, nullIfEmpty(derefString(req.Tagline, "")), shortDescription,
		nullIfEmpty(derefString(req.FullDescription, "")), nullIfEmpty(derefString(req.County, "")),
		nullIfEmpty(derefString(req.Locality, "")), nullIfEmpty(derefString(req.ServiceArea, "")),
		nullIfEmpty(derefString(req.Address, "")), req.LocationLat.Value, req.LocationLng.Value,
		nullIfEmpty(derefString(req.ContactEmail, "")), nullIfEmpty(derefString(req.ContactPhone, "")),
		redirectURL, defaultIfEmpty(derefString(req.RedirectLabel, ""), "Visit partner"), nullIfEmpty(derefString(req.Disclosure, "")),
		nullIfEmpty(derefString(req.ImageURL, "")), nullIfEmpty(derefString(req.ImageAlt, "")),
		nullIfEmpty(derefString(req.ImageCredit, "")), status, derefBool(req.IsFeatured, false), displayOrder,
		nullableUserID(middleware.UserID(r.Context())),
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create business", err)
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *MarketplaceAdminHandler) UpdateBusiness(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid business id")
		return
	}

	var existing adminBusiness
	err := scanBusiness(h.pool.QueryRow(r.Context(), `SELECT `+businessColumns+` FROM partner_businesses WHERE id = $1`, id), &existing)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "business not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load business", err)
		return
	}

	var req businessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, existing.Name)
	slug := derefString(req.Slug, existing.Slug)
	category := derefString(req.Category, existing.Category)
	shortDescription := derefString(req.ShortDescription, existing.ShortDescription)
	redirectURL := derefString(req.RedirectURL, existing.RedirectURL)
	status := derefString(req.Status, existing.Status)
	displayOrder := derefInt(req.DisplayOrder, existing.DisplayOrder)
	locationLat := existing.LocationLat
	if req.LocationLat.Set {
		locationLat = req.LocationLat.Value
	}
	locationLng := existing.LocationLng
	if req.LocationLng.Set {
		locationLng = req.LocationLng.Value
	}

	if err := validateBusiness(name, slug, category, shortDescription, redirectURL, status, displayOrder); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if !validOptionalEmail(derefString(req.ContactEmail, "")) {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "contactEmail is not a valid email address")
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		UPDATE partner_businesses SET
			name=$1, slug=$2, category=$3, tagline=$4, short_description=$5, full_description=$6,
			county=$7, locality=$8, service_area=$9, address=$10, location_lat=$11, location_lng=$12,
			contact_email=$13, contact_phone=$14, redirect_url=$15, redirect_label=$16, disclosure=$17,
			image_url=$18, image_alt=$19, image_credit=$20, status=$21, is_featured=$22, display_order=$23,
			updated_at=now()
		WHERE id=$24`,
		name, slug, category, derefNullableString(req.Tagline, existing.Tagline), shortDescription,
		derefNullableString(req.FullDescription, existing.FullDescription),
		derefNullableString(req.County, existing.County), derefNullableString(req.Locality, existing.Locality),
		derefNullableString(req.ServiceArea, existing.ServiceArea), derefNullableString(req.Address, existing.Address),
		locationLat, locationLng,
		derefNullableString(req.ContactEmail, existing.ContactEmail),
		derefNullableString(req.ContactPhone, existing.ContactPhone), redirectURL,
		derefNullableString(req.RedirectLabel, existing.RedirectLabel),
		derefNullableString(req.Disclosure, existing.Disclosure),
		derefNullableString(req.ImageURL, existing.ImageURLKey),
		derefNullableString(req.ImageAlt, existing.ImageAlt),
		derefNullableString(req.ImageCredit, existing.ImageCredit),
		status, derefBool(req.IsFeatured, existing.IsFeatured), displayOrder, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update business", err)
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *MarketplaceAdminHandler) DeleteBusiness(w http.ResponseWriter, r *http.Request) {
	deleteByID(w, r, h.pool, "partner_businesses", "business")
}

func validateBusiness(name, slug, category, shortDescription, redirectURL, status string, displayOrder int) error {
	if len(strings.TrimSpace(name)) < 2 {
		return fmt.Errorf("name must be at least 2 characters")
	}
	if len(name) > 120 {
		return fmt.Errorf("name must be 120 characters or fewer")
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must be lowercase letters, numbers, and hyphens")
	}
	if !validCategory[category] {
		return fmt.Errorf("invalid category")
	}
	if shortDescription == "" {
		return fmt.Errorf("shortDescription is required")
	}
	if len(shortDescription) > 240 {
		return fmt.Errorf("shortDescription must be 240 characters or fewer")
	}
	if !validHTTPURL(redirectURL) {
		return fmt.Errorf("redirectUrl must be a valid http(s) URL")
	}
	if !validStatus[status] {
		return fmt.Errorf("invalid status")
	}
	if displayOrder < 0 || displayOrder > 10000 {
		return fmt.Errorf("displayOrder must be between 0 and 10000")
	}
	return nil
}

// ── Accommodation properties ──

type stayRequest struct {
	Name             *string           `json:"name"`
	Slug             *string           `json:"slug"`
	Tier             *string           `json:"tier"`
	PropertyType     *string           `json:"propertyType"`
	ShortDescription *string           `json:"shortDescription"`
	FullDescription  *string           `json:"fullDescription"`
	County           *string           `json:"county"`
	Locality         *string           `json:"locality"`
	Address          *string           `json:"address"`
	LocationLat      optional[float64] `json:"locationLat"`
	LocationLng      optional[float64] `json:"locationLng"`
	PriceFromKES     optional[int]     `json:"priceFromKes"`
	PriceNote        *string           `json:"priceNote"`
	PriceCheckedAt   *string           `json:"priceCheckedAt"`
	DistanceNote     *string           `json:"distanceNote"`
	CancellationNote *string           `json:"cancellationNote"`
	PaymentNote      *string           `json:"paymentNote"`
	IncludedNote     *string           `json:"includedNote"`
	BadgeLabel       *string           `json:"badgeLabel"`
	Amenities        []string          `json:"amenities"`
	Highlights       []string          `json:"highlights"`
	BookingURL       *string           `json:"bookingUrl"`
	DirectURL        *string           `json:"directUrl"`
	Disclosure       *string           `json:"disclosure"`
	ContactEmail     *string           `json:"contactEmail"`
	ContactPhone     *string           `json:"contactPhone"`
	ImageURL         *string           `json:"imageUrl"`
	ImageAlt         *string           `json:"imageAlt"`
	ImageCredit      *string           `json:"imageCredit"`
	Status           *string           `json:"status"`
	IsFeatured       *bool             `json:"isFeatured"`
	DisplayOrder     *int              `json:"displayOrder"`
}

type adminStay struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Slug             string   `json:"slug"`
	Tier             string   `json:"tier"`
	PropertyType     string   `json:"propertyType"`
	ShortDescription string   `json:"shortDescription"`
	FullDescription  *string  `json:"fullDescription"`
	County           string   `json:"county"`
	Locality         *string  `json:"locality"`
	Address          *string  `json:"address"`
	LocationLat      *float64 `json:"locationLat"`
	LocationLng      *float64 `json:"locationLng"`
	PriceFromKES     *int     `json:"priceFromKes"`
	PriceNote        *string  `json:"priceNote"`
	PriceCheckedAt   *string  `json:"priceCheckedAt"`
	DistanceNote     *string  `json:"distanceNote"`
	CancellationNote *string  `json:"cancellationNote"`
	PaymentNote      *string  `json:"paymentNote"`
	IncludedNote     *string  `json:"includedNote"`
	BadgeLabel       *string  `json:"badgeLabel"`
	Amenities        []string `json:"amenities"`
	Highlights       []string `json:"highlights"`
	BookingURL       *string  `json:"bookingUrl"`
	DirectURL        *string  `json:"directUrl"`
	Disclosure       *string  `json:"disclosure"`
	ContactEmail     *string  `json:"contactEmail"`
	ContactPhone     *string  `json:"contactPhone"`
	ImageURL         *string  `json:"imageUrl"`
	ImageURLKey      *string  `json:"imageUrlKey"`
	ImageAlt         *string  `json:"imageAlt"`
	ImageCredit      *string  `json:"imageCredit"`
	Status           string   `json:"status"`
	IsFeatured       bool     `json:"isFeatured"`
	DisplayOrder     int      `json:"displayOrder"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

func (s adminStay) CursorValues() (string, string) {
	return strconv.Itoa(s.DisplayOrder), s.ID
}

func scanStay(row pgx.Row, s *adminStay) error {
	var imageKey *string
	err := row.Scan(&s.ID, &s.Name, &s.Slug, &s.Tier, &s.PropertyType, &s.ShortDescription,
		&s.FullDescription, &s.County, &s.Locality, &s.Address, &s.LocationLat, &s.LocationLng,
		&s.PriceFromKES, &s.PriceNote, &s.PriceCheckedAt, &s.DistanceNote, &s.CancellationNote,
		&s.PaymentNote, &s.IncludedNote, &s.BadgeLabel, &s.Amenities, &s.Highlights,
		&s.BookingURL, &s.DirectURL, &s.Disclosure, &s.ContactEmail, &s.ContactPhone,
		&imageKey, &s.ImageAlt, &s.ImageCredit, &s.Status, &s.IsFeatured, &s.DisplayOrder,
		&s.CreatedAt, &s.UpdatedAt)
	s.ImageURLKey = imageKey
	return err
}

const stayColumns = `id::text, name, slug, tier, property_type, short_description, full_description,
	county, locality, address, location_lat, location_lng, price_from_kes, price_note, price_checked_at::text,
	distance_note, cancellation_note, payment_note, included_note, badge_label, amenities,
	highlights, booking_url, direct_url, disclosure, contact_email, contact_phone, image_url,
	image_alt, image_credit, status, is_featured, display_order, created_at::text, updated_at::text`

func (h *MarketplaceAdminHandler) ListStays(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	args := []any{}
	where := []string{"1 = 1"}

	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		where = append(where, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, status)
	}
	if tier := strings.TrimSpace(r.URL.Query().Get("tier")); tier != "" {
		where = append(where, fmt.Sprintf("tier = $%d", len(args)+1))
		args = append(args, tier)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		n := len(args) + 1
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR slug ILIKE $%d OR county ILIKE $%d)", n, n, n))
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

	query := `SELECT ` + stayColumns + `
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

	items := make([]adminStay, 0, pr.Limit)
	for rows.Next() {
		var s adminStay
		if err := scanStay(rows, &s); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan accommodation", err)
			return
		}
		if s.Amenities == nil {
			s.Amenities = []string{}
		}
		if s.Highlights == nil {
			s.Highlights = []string{}
		}
		s.ImageURL = h.presign(r.Context(), s.ImageURLKey)
		items = append(items, s)
	}

	writeAdminPage(w, items, pr.Limit, func(s adminStay) (string, string) { return s.CursorValues() })
}

func (h *MarketplaceAdminHandler) GetStay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid accommodation id")
		return
	}
	var s adminStay
	err := scanStay(h.pool.QueryRow(r.Context(), `SELECT `+stayColumns+` FROM accommodation_properties WHERE id = $1`, id), &s)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "accommodation not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch accommodation", err)
		return
	}
	if s.Amenities == nil {
		s.Amenities = []string{}
	}
	if s.Highlights == nil {
		s.Highlights = []string{}
	}
	s.ImageURL = h.presign(r.Context(), s.ImageURLKey)
	writeAdminJSON(w, http.StatusOK, s)
}

func (h *MarketplaceAdminHandler) CreateStay(w http.ResponseWriter, r *http.Request) {
	var req stayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, "")
	slug := derefString(req.Slug, "")
	tier := derefString(req.Tier, "")
	propertyType := derefString(req.PropertyType, "")
	shortDescription := derefString(req.ShortDescription, "")
	county := derefString(req.County, "")
	status := derefString(req.Status, "draft")
	displayOrder := derefInt(req.DisplayOrder, 0)
	bookingURL := derefString(req.BookingURL, "")
	directURL := derefString(req.DirectURL, "")
	priceFromKES := req.PriceFromKES.Value
	contactEmail := derefString(req.ContactEmail, "")

	if err := validateStay(name, slug, tier, propertyType, shortDescription, county, status, displayOrder,
		bookingURL, directURL, priceFromKES, contactEmail); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	var id string
	amenities := req.Amenities
	if amenities == nil {
		amenities = []string{}
	}
	highlights := req.Highlights
	if highlights == nil {
		highlights = []string{}
	}
	if len(highlights) > 6 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "add at most 6 highlights")
		return
	}
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO accommodation_properties (
			name, slug, tier, property_type, short_description, full_description, county, locality,
			address, location_lat, location_lng, price_from_kes, price_note, price_checked_at,
			distance_note, cancellation_note, payment_note, included_note, badge_label,
			amenities, highlights, booking_url, direct_url, disclosure, contact_email, contact_phone,
			image_url, image_alt, image_credit, status, is_featured, display_order, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)
		RETURNING id::text`,
		name, slug, tier, propertyType, shortDescription, nullIfEmpty(derefString(req.FullDescription, "")),
		county, nullIfEmpty(derefString(req.Locality, "")), nullIfEmpty(derefString(req.Address, "")),
		req.LocationLat.Value, req.LocationLng.Value, priceFromKES,
		nullIfEmpty(derefString(req.PriceNote, "")), nullIfEmpty(derefString(req.PriceCheckedAt, "")),
		nullIfEmpty(derefString(req.DistanceNote, "")), nullIfEmpty(derefString(req.CancellationNote, "")),
		nullIfEmpty(derefString(req.PaymentNote, "")), nullIfEmpty(derefString(req.IncludedNote, "")),
		nullIfEmpty(derefString(req.BadgeLabel, "")), amenities, highlights,
		nullIfEmpty(bookingURL), nullIfEmpty(directURL), nullIfEmpty(derefString(req.Disclosure, "")),
		nullIfEmpty(contactEmail), nullIfEmpty(derefString(req.ContactPhone, "")),
		nullIfEmpty(derefString(req.ImageURL, "")), nullIfEmpty(derefString(req.ImageAlt, "")),
		nullIfEmpty(derefString(req.ImageCredit, "")), status, derefBool(req.IsFeatured, false), displayOrder,
		nullableUserID(middleware.UserID(r.Context())),
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create accommodation", err)
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *MarketplaceAdminHandler) UpdateStay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid accommodation id")
		return
	}

	var existing adminStay
	err := scanStay(h.pool.QueryRow(r.Context(), `SELECT `+stayColumns+` FROM accommodation_properties WHERE id = $1`, id), &existing)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "accommodation not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load accommodation", err)
		return
	}

	var req stayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, existing.Name)
	slug := derefString(req.Slug, existing.Slug)
	tier := derefString(req.Tier, existing.Tier)
	propertyType := derefString(req.PropertyType, existing.PropertyType)
	shortDescription := derefString(req.ShortDescription, existing.ShortDescription)
	county := derefString(req.County, existing.County)
	status := derefString(req.Status, existing.Status)
	displayOrder := derefInt(req.DisplayOrder, existing.DisplayOrder)
	bookingURL := derefString(req.BookingURL, derefString(existing.BookingURL, ""))
	directURL := derefString(req.DirectURL, derefString(existing.DirectURL, ""))
	priceFromKES := existing.PriceFromKES
	if req.PriceFromKES.Set {
		priceFromKES = req.PriceFromKES.Value
	}
	locationLat := existing.LocationLat
	if req.LocationLat.Set {
		locationLat = req.LocationLat.Value
	}
	locationLng := existing.LocationLng
	if req.LocationLng.Set {
		locationLng = req.LocationLng.Value
	}
	contactEmail := derefString(req.ContactEmail, derefString(existing.ContactEmail, ""))

	if err := validateStay(name, slug, tier, propertyType, shortDescription, county, status, displayOrder,
		bookingURL, directURL, priceFromKES, contactEmail); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	amenities := existing.Amenities
	if req.Amenities != nil {
		amenities = req.Amenities
	}
	if amenities == nil {
		amenities = []string{}
	}
	highlights := existing.Highlights
	if req.Highlights != nil {
		highlights = req.Highlights
	}
	if highlights == nil {
		highlights = []string{}
	}
	if len(highlights) > 6 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "add at most 6 highlights")
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		UPDATE accommodation_properties SET
			name=$1, slug=$2, tier=$3, property_type=$4, short_description=$5, full_description=$6,
			county=$7, locality=$8, address=$9, location_lat=$10, location_lng=$11, price_from_kes=$12,
			price_note=$13, price_checked_at=$14, distance_note=$15, cancellation_note=$16,
			payment_note=$17, included_note=$18, badge_label=$19, amenities=$20, highlights=$21,
			booking_url=$22, direct_url=$23, disclosure=$24, contact_email=$25, contact_phone=$26,
			image_url=$27, image_alt=$28, image_credit=$29, status=$30, is_featured=$31,
			display_order=$32, updated_at=now()
		WHERE id=$33`,
		name, slug, tier, propertyType, shortDescription,
		derefNullableString(req.FullDescription, existing.FullDescription), county,
		derefNullableString(req.Locality, existing.Locality), derefNullableString(req.Address, existing.Address),
		locationLat, locationLng,
		priceFromKES, derefNullableString(req.PriceNote, existing.PriceNote),
		derefNullableString(req.PriceCheckedAt, existing.PriceCheckedAt),
		derefNullableString(req.DistanceNote, existing.DistanceNote),
		derefNullableString(req.CancellationNote, existing.CancellationNote),
		derefNullableString(req.PaymentNote, existing.PaymentNote),
		derefNullableString(req.IncludedNote, existing.IncludedNote),
		derefNullableString(req.BadgeLabel, existing.BadgeLabel), amenities, highlights,
		nullIfEmpty(bookingURL), nullIfEmpty(directURL), derefNullableString(req.Disclosure, existing.Disclosure),
		nullIfEmpty(contactEmail), derefNullableString(req.ContactPhone, existing.ContactPhone),
		derefNullableString(req.ImageURL, existing.ImageURLKey),
		derefNullableString(req.ImageAlt, existing.ImageAlt),
		derefNullableString(req.ImageCredit, existing.ImageCredit), status,
		derefBool(req.IsFeatured, existing.IsFeatured), displayOrder, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update accommodation", err)
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *MarketplaceAdminHandler) DeleteStay(w http.ResponseWriter, r *http.Request) {
	deleteByID(w, r, h.pool, "accommodation_properties", "accommodation")
}

func validateStay(name, slug, tier, propertyType, shortDescription, county, status string, displayOrder int,
	bookingURL, directURL string, priceFromKES *int, contactEmail string) error {
	if len(strings.TrimSpace(name)) < 2 {
		return fmt.Errorf("name must be at least 2 characters")
	}
	if len(name) > 120 {
		return fmt.Errorf("name must be 120 characters or fewer")
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must be lowercase letters, numbers, and hyphens")
	}
	if !validTier[tier] {
		return fmt.Errorf("invalid tier")
	}
	if !validPropertyType[propertyType] {
		return fmt.Errorf("invalid propertyType")
	}
	if shortDescription == "" {
		return fmt.Errorf("shortDescription is required")
	}
	if len(shortDescription) > 240 {
		return fmt.Errorf("shortDescription must be 240 characters or fewer")
	}
	if county == "" {
		return fmt.Errorf("county is required")
	}
	if !validStatus[status] {
		return fmt.Errorf("invalid status")
	}
	if displayOrder < 0 || displayOrder > 10000 {
		return fmt.Errorf("displayOrder must be between 0 and 10000")
	}
	if priceFromKES != nil && *priceFromKES < 0 {
		return fmt.Errorf("priceFromKes must be zero or greater")
	}
	if bookingURL != "" && !validHTTPURL(bookingURL) {
		return fmt.Errorf("bookingUrl must be a valid http(s) URL")
	}
	if directURL != "" && !validHTTPURL(directURL) {
		return fmt.Errorf("directUrl must be a valid http(s) URL")
	}
	if status == "published" && bookingURL == "" && directURL == "" {
		return fmt.Errorf("a published stay needs a bookingUrl or directUrl")
	}
	if !validOptionalEmail(contactEmail) {
		return fmt.Errorf("contactEmail is not a valid email address")
	}
	return nil
}

// ── eSIM providers ──

type esimRequest struct {
	Name          *string `json:"name"`
	Slug          *string `json:"slug"`
	Headline      *string `json:"headline"`
	Description   *string `json:"description"`
	CoverageNote  *string `json:"coverageNote"`
	PriceLabel    *string `json:"priceLabel"`
	RedirectURL   *string `json:"redirectUrl"`
	RedirectLabel *string `json:"redirectLabel"`
	Disclosure    *string `json:"disclosure"`
	LogoURL       *string `json:"logoUrl"`
	LogoAlt       *string `json:"logoAlt"`
	Status        *string `json:"status"`
	IsFeatured    *bool   `json:"isFeatured"`
	DisplayOrder  *int    `json:"displayOrder"`
}

type adminEsim struct {
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
	LogoURLKey    *string `json:"logoUrlKey"`
	LogoAlt       *string `json:"logoAlt"`
	Status        string  `json:"status"`
	IsFeatured    bool    `json:"isFeatured"`
	DisplayOrder  int     `json:"displayOrder"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

func (e adminEsim) CursorValues() (string, string) {
	return strconv.Itoa(e.DisplayOrder), e.ID
}

func scanEsim(row pgx.Row, e *adminEsim) error {
	var logoKey *string
	err := row.Scan(&e.ID, &e.Name, &e.Slug, &e.Headline, &e.Description, &e.CoverageNote,
		&e.PriceLabel, &e.RedirectURL, &e.RedirectLabel, &e.Disclosure, &logoKey, &e.LogoAlt,
		&e.Status, &e.IsFeatured, &e.DisplayOrder, &e.CreatedAt, &e.UpdatedAt)
	e.LogoURLKey = logoKey
	return err
}

const esimColumns = `id::text, name, slug, headline, description, coverage_note, price_label,
	redirect_url, redirect_label, disclosure, logo_url, logo_alt, status, is_featured,
	display_order, created_at::text, updated_at::text`

func (h *MarketplaceAdminHandler) ListEsimProviders(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	args := []any{}
	where := []string{"1 = 1"}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		where = append(where, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, status)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		n := len(args) + 1
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR slug ILIKE $%d OR COALESCE(headline, '') ILIKE $%d)", n, n, n))
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
	query := `SELECT ` + esimColumns + `
	          FROM esim_providers
	          WHERE ` + strings.Join(where, " AND ") + `
	          ORDER BY display_order ASC, id ASC
	          LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, pr.Limit+1)

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list eSIM providers", err)
		return
	}
	defer rows.Close()

	items := make([]adminEsim, 0, pr.Limit)
	for rows.Next() {
		var e adminEsim
		if err := scanEsim(rows, &e); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan eSIM provider", err)
			return
		}
		e.LogoURL = h.presign(r.Context(), e.LogoURLKey)
		items = append(items, e)
	}

	writeAdminPage(w, items, pr.Limit, func(e adminEsim) (string, string) { return e.CursorValues() })
}

func (h *MarketplaceAdminHandler) GetEsimProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid eSIM provider id")
		return
	}
	var e adminEsim
	err := scanEsim(h.pool.QueryRow(r.Context(), `SELECT `+esimColumns+` FROM esim_providers WHERE id = $1`, id), &e)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "eSIM provider not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch eSIM provider", err)
		return
	}
	e.LogoURL = h.presign(r.Context(), e.LogoURLKey)
	writeAdminJSON(w, http.StatusOK, e)
}

func (h *MarketplaceAdminHandler) CreateEsimProvider(w http.ResponseWriter, r *http.Request) {
	var req esimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, "")
	slug := derefString(req.Slug, "")
	description := derefString(req.Description, "")
	redirectURL := derefString(req.RedirectURL, "")
	status := derefString(req.Status, "draft")
	displayOrder := derefInt(req.DisplayOrder, 0)

	if err := validateEsim(name, slug, description, redirectURL, status, displayOrder); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if len(derefString(req.Headline, "")) > 120 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "headline must be 120 characters or fewer")
		return
	}
	if derefBool(req.IsFeatured, false) && status != "published" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "a featured eSIM provider must be published")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	if derefBool(req.IsFeatured, false) {
		if _, err := tx.Exec(r.Context(), `UPDATE esim_providers SET is_featured = false WHERE is_featured = true`); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear featured eSIM provider", err)
			return
		}
	}

	var id string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO esim_providers (
			name, slug, headline, description, coverage_note, price_label, redirect_url,
			redirect_label, disclosure, logo_url, logo_alt, status, is_featured, display_order, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id::text`,
		name, slug, nullIfEmpty(derefString(req.Headline, "")), description,
		nullIfEmpty(derefString(req.CoverageNote, "")), nullIfEmpty(derefString(req.PriceLabel, "")),
		redirectURL, defaultIfEmpty(derefString(req.RedirectLabel, ""), "Get connected"),
		nullIfEmpty(derefString(req.Disclosure, "")), nullIfEmpty(derefString(req.LogoURL, "")),
		nullIfEmpty(derefString(req.LogoAlt, "")), status, derefBool(req.IsFeatured, false), displayOrder,
		nullableUserID(middleware.UserID(r.Context())),
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists or another provider is featured")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create eSIM provider", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit eSIM provider", err)
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *MarketplaceAdminHandler) UpdateEsimProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid eSIM provider id")
		return
	}

	var existing adminEsim
	err := scanEsim(h.pool.QueryRow(r.Context(), `SELECT `+esimColumns+` FROM esim_providers WHERE id = $1`, id), &existing)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "eSIM provider not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load eSIM provider", err)
		return
	}

	var req esimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	name := derefString(req.Name, existing.Name)
	slug := derefString(req.Slug, existing.Slug)
	description := derefString(req.Description, existing.Description)
	redirectURL := derefString(req.RedirectURL, existing.RedirectURL)
	status := derefString(req.Status, existing.Status)
	displayOrder := derefInt(req.DisplayOrder, existing.DisplayOrder)
	isFeatured := derefBool(req.IsFeatured, existing.IsFeatured)

	if err := validateEsim(name, slug, description, redirectURL, status, displayOrder); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if len(derefString(req.Headline, "")) > 120 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "headline must be 120 characters or fewer")
		return
	}
	if isFeatured && status != "published" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "a featured eSIM provider must be published")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	if isFeatured {
		if _, err := tx.Exec(r.Context(), `UPDATE esim_providers SET is_featured = false WHERE is_featured = true AND id <> $1`, id); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear featured eSIM provider", err)
			return
		}
	}

	_, err = tx.Exec(r.Context(), `
		UPDATE esim_providers SET
			name=$1, slug=$2, headline=$3, description=$4, coverage_note=$5, price_label=$6,
			redirect_url=$7, redirect_label=$8, disclosure=$9, logo_url=$10, logo_alt=$11,
			status=$12, is_featured=$13, display_order=$14, updated_at=now()
		WHERE id=$15`,
		name, slug, derefNullableString(req.Headline, existing.Headline), description,
		derefNullableString(req.CoverageNote, existing.CoverageNote),
		derefNullableString(req.PriceLabel, existing.PriceLabel), redirectURL,
		derefNullableString(req.RedirectLabel, existing.RedirectLabel),
		derefNullableString(req.Disclosure, existing.Disclosure),
		derefNullableString(req.LogoURL, existing.LogoURLKey),
		derefNullableString(req.LogoAlt, existing.LogoAlt), status, isFeatured, displayOrder, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT", "slug already exists or another provider is featured")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update eSIM provider", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit eSIM provider", err)
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *MarketplaceAdminHandler) DeleteEsimProvider(w http.ResponseWriter, r *http.Request) {
	deleteByID(w, r, h.pool, "esim_providers", "eSIM provider")
}

func validateEsim(name, slug, description, redirectURL, status string, displayOrder int) error {
	if len(strings.TrimSpace(name)) < 2 {
		return fmt.Errorf("name must be at least 2 characters")
	}
	if len(name) > 120 {
		return fmt.Errorf("name must be 120 characters or fewer")
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must be lowercase letters, numbers, and hyphens")
	}
	if description == "" {
		return fmt.Errorf("description is required")
	}
	if len(description) > 400 {
		return fmt.Errorf("description must be 400 characters or fewer")
	}
	if !validHTTPURL(redirectURL) {
		return fmt.Errorf("redirectUrl must be a valid http(s) URL")
	}
	if !validStatus[status] {
		return fmt.Errorf("invalid status")
	}
	if displayOrder < 0 || displayOrder > 10000 {
		return fmt.Errorf("displayOrder must be between 0 and 10000")
	}
	return nil
}

// ── shared helpers ──

func nullableUserID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func deleteByID(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, table, label string) {
	id := r.PathValue("id")
	if !isUUID(id) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "invalid "+label+" id")
		return
	}
	tag, err := pool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, table), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete "+label, err)
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", label+" not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeAdminPage[T any](w http.ResponseWriter, items []T, limit int, cursor func(T) (string, string)) {
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
	writeAdminJSON(w, http.StatusOK, resp)
}
