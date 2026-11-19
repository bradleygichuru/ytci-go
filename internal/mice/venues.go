package mice

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// loadVenue fetches a venue by id or slug and attaches its accessibility
// report.
func (h *Handler) loadVenue(ctx context.Context, by, value string) (*Venue, error) {
	column := "v.slug"
	if by == "id" {
		column = "v.id::text"
	}
	var v Venue
	var id, slug, name, address1, address2, county, transport, dropoff, parking pgtype.Text
	var countyGid pgtype.Int4
	var lat, lng pgtype.Numeric
	var capPerson, capOnline pgtype.Int4
	var rooms []string
	var verified pgtype.Timestamptz
	err := h.pool.QueryRow(ctx, `
		SELECT v.id::text, v.slug, v.name, v.address_line1, v.address_line2,
		       COALESCE(v.county,''), v.county_gid, v.latitude, v.longitude,
		       v.capacity_in_person, v.capacity_online, v.rooms,
		       v.transport_notes, v.dropoff_notes, v.parking_notes, v.verified_at
		  FROM venues v WHERE `+column+` = $1`, value).Scan(
		&id, &slug, &name, &address1, &address2, &county, &countyGid, &lat, &lng,
		&capPerson, &capOnline, &rooms, &transport, &dropoff, &parking, &verified,
	)
	if err != nil {
		return nil, err
	}
	v = Venue{
		VenueRef: VenueRef{
			Slug: slug.String, Name: name.String, County: county.String,
			CountyGid: nullInt(countyGid),
		},
		ID:               id.String,
		AddressLine1:     nullText(address1),
		AddressLine2:     nullText(address2),
		Latitude:         nullNumeric(lat),
		Longitude:        nullNumeric(lng),
		CapacityInPerson: nullInt(capPerson),
		CapacityOnline:   nullInt(capOnline),
		Rooms:            rooms,
		TransportNotes:   nullText(transport),
		DropoffNotes:     nullText(dropoff),
		ParkingNotes:     nullText(parking),
		VerifiedAt:       nullTime(verified),
	}
	block, err := h.loadAccessibility(ctx, "venue", v.ID)
	if err != nil {
		return nil, err
	}
	v.Accessibility = block
	return &v, nil
}

func (h *Handler) handleGetPublicVenue(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !slugPattern.MatchString(slug) {
		writeErr(w, r, http.StatusNotFound, "VENUE_NOT_FOUND", "venue not found", nil)
		return
	}
	v, err := h.loadVenue(r.Context(), "slug", slug)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "VENUE_NOT_FOUND", "venue not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type venueWrite struct {
	Slug             string              `json:"slug"`
	Name             string              `json:"name"`
	County           string              `json:"county"`
	CountyGid        *int                `json:"countyGid"`
	AddressLine1     string              `json:"addressLine1"`
	AddressLine2     string              `json:"addressLine2"`
	Latitude         *float64            `json:"latitude"`
	Longitude        *float64            `json:"longitude"`
	CapacityInPerson *int                `json:"capacityInPerson"`
	CapacityOnline   *int                `json:"capacityOnline"`
	Rooms            []string            `json:"rooms"`
	TransportNotes   string              `json:"transportNotes"`
	DropoffNotes     string              `json:"dropoffNotes"`
	ParkingNotes     string              `json:"parkingNotes"`
	Accessibility    *accessibilityWrite `json:"accessibility"`
}

func (v venueWrite) validate() []ValidationError {
	var out []ValidationError
	if strings.TrimSpace(v.Name) == "" {
		out = append(out, ValidationError{Field: "name", Message: "name is required"})
	}
	if strings.TrimSpace(v.County) == "" {
		out = append(out, ValidationError{Field: "county", Message: "county is required"})
	}
	if v.Slug != "" && !slugPattern.MatchString(v.Slug) {
		out = append(out, ValidationError{Field: "slug", Message: "must match ^[a-z0-9-]+$"})
	}
	return out
}

func (h *Handler) handleAdminListVenues(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.read") {
		return
	}
	limit, cursor := parsePage(r)
	args := []any{}
	q := `
		SELECT v.id::text, v.slug, v.name, COALESCE(v.county,''), v.county_gid,
		       v.capacity_in_person, v.capacity_online, v.verified_at
		  FROM venues v WHERE 1=1`
	if gid := r.URL.Query().Get("countyGid"); gid != "" {
		if n, err := strconv.Atoi(gid); err == nil {
			args = append(args, n)
			q += fmt.Sprintf(" AND v.county_gid = $%d", len(args))
		}
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (v.name, v.id) > ($%d, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY v.name ASC, v.id ASC LIMIT $%d", len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items := []Venue{}
	for rows.Next() {
		var v Venue
		var id, slug, name, county pgtype.Text
		var gid, capPerson, capOnline pgtype.Int4
		var verified pgtype.Timestamptz
		if err := rows.Scan(&id, &slug, &name, &county, &gid, &capPerson, &capOnline, &verified); err != nil {
			writeServer(w, r, err)
			return
		}
		v = Venue{
			VenueRef: VenueRef{Slug: slug.String, Name: name.String, County: county.String, CountyGid: nullInt(gid)},
			ID:       id.String, CapacityInPerson: nullInt(capPerson), CapacityOnline: nullInt(capOnline),
			VerifiedAt: nullTime(verified),
		}
		items = append(items, v)
	}
	writePage(w, limit, items, func(v Venue) (string, bool) {
		return paginationEncode(v.Name, v.ID), v.ID != ""
	})
}

func (h *Handler) handleAdminCreateVenue(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.create") {
		return
	}
	var body venueWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	slug := body.Slug
	if slug == "" {
		slug = slugify(body.Name)
	}
	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO venues (slug, name, county, county_gid, address_line1, address_line2,
			latitude, longitude, capacity_in_person, capacity_online, rooms,
			transport_notes, dropoff_notes, parking_notes, created_by, verified_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,COALESCE($11::jsonb,'[]'::jsonb),NULLIF($12,''),
		       NULLIF($13,''),NULLIF($14,''),$15,now())
		RETURNING id::text`,
		slug, body.Name, body.County, body.CountyGid, body.AddressLine1, body.AddressLine2,
		body.Latitude, body.Longitude, body.CapacityInPerson, body.CapacityOnline, body.Rooms,
		body.TransportNotes, body.DropoffNotes, body.ParkingNotes, userID(r)).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "venues_slug_key") {
			writeErr(w, r, http.StatusConflict, "VENUE_SLUG_TAKEN", "a venue already uses this slug", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if err := h.upsertAccessibility(r.Context(), "venue", id, body.Accessibility, userID(r)); err != nil {
		writeServer(w, r, err)
		return
	}
	v, err := h.loadVenue(r.Context(), "id", id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) handleAdminUpdateVenue(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.create") {
		return
	}
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeErr(w, r, http.StatusNotFound, "VENUE_NOT_FOUND", "venue not found", nil)
		return
	}
	var body venueWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	slug := body.Slug
	if slug == "" {
		slug = slugify(body.Name)
	}
	tag, err := h.pool.Exec(r.Context(), `
		UPDATE venues SET slug=$2, name=$3, county=$4, county_gid=$5,
			address_line1=NULLIF($6,''), address_line2=NULLIF($7,''),
			latitude=$8, longitude=$9, capacity_in_person=$10, capacity_online=$11,
			rooms=COALESCE($12::jsonb,'[]'::jsonb), transport_notes=NULLIF($13,''), dropoff_notes=NULLIF($14,''),
			parking_notes=NULLIF($15,''), verified_at=now(), updated_at=now()
		WHERE id=$1`,
		id, slug, body.Name, body.County, body.CountyGid, body.AddressLine1, body.AddressLine2,
		body.Latitude, body.Longitude, body.CapacityInPerson, body.CapacityOnline, body.Rooms,
		body.TransportNotes, body.DropoffNotes, body.ParkingNotes)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, r, http.StatusNotFound, "VENUE_NOT_FOUND", "venue not found", nil)
		return
	}
	if err := h.upsertAccessibility(r.Context(), "venue", id, body.Accessibility, userID(r)); err != nil {
		writeServer(w, r, err)
		return
	}
	v, err := h.loadVenue(r.Context(), "id", id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// venueByID exists for callers that only need existence checks.
func (h *Handler) venueExists(ctx context.Context, id string) bool {
	var exists bool
	if err := h.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM venues WHERE id=$1)`, id).Scan(&exists); err != nil {
		return false
	}
	return exists
}

var _ = pgx.ErrNoRows
