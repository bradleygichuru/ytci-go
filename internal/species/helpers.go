package species

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

// sourceScan mirrors the nullable columns of a joined species_sources row.
type sourceScan struct {
	ID           sql.NullString
	Publisher    sql.NullString
	Citation     sql.NullString
	URL          sql.NullString
	AccessedAt   sql.NullTime
	LicenceCode  sql.NullString
	LicenceURL   sql.NullString
	PermittedUse sql.NullString
	Attribution  sql.NullString
	SnapshotHash sql.NullString
	ReviewStatus sql.NullString
}

func (s sourceScan) toSource() *Source {
	if !s.ID.Valid {
		return nil
	}
	out := &Source{
		ID:           s.ID.String,
		Publisher:    s.Publisher.String,
		Citation:     s.Citation.String,
		URL:          nullString(s.URL),
		LicenceCode:  s.LicenceCode.String,
		LicenceURL:   nullString(s.LicenceURL),
		PermittedUse: s.PermittedUse.String,
		Attribution:  s.Attribution.String,
		SnapshotHash: nullString(s.SnapshotHash),
		ReviewStatus: s.ReviewStatus.String,
	}
	if s.AccessedAt.Valid {
		out.AccessedAt = s.AccessedAt.Time.Format("2006-01-02")
	}
	return out
}

type rightsScan struct {
	ID            sql.NullString
	SubjectType   sql.NullString
	SubjectID     sql.NullString
	RightsHolder  sql.NullString
	LicenceCode   sql.NullString
	LicenceURL    sql.NullString
	PermittedUse  sql.NullString
	Attribution   sql.NullString
	ConsentRecord sql.NullString
	ExpiresAt     sql.NullTime
	WithdrawnAt   sql.NullTime
}

func (r rightsScan) toRights() *Rights {
	if !r.ID.Valid {
		return nil
	}
	out := &Rights{
		ID:            r.ID.String,
		SubjectType:   r.SubjectType.String,
		SubjectID:     r.SubjectID.String,
		RightsHolder:  r.RightsHolder.String,
		LicenceCode:   r.LicenceCode.String,
		LicenceURL:    nullString(r.LicenceURL),
		PermittedUse:  r.PermittedUse.String,
		Attribution:   r.Attribution.String,
		ConsentRecord: nullString(r.ConsentRecord),
	}
	if r.ExpiresAt.Valid {
		t := r.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	if r.WithdrawnAt.Valid {
		t := r.WithdrawnAt.Time
		out.WithdrawnAt = &t
	}
	return out
}

func nullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// mediaURL presigns a managed object key when storage is configured. Without
// storage the key is returned unchanged so dev and tests still exercise the
// full shape. No external URL is ever stored (species_media_no_external_check).
func (r *Repo) mediaURL(ctx context.Context, key sql.NullString) *string {
	if !key.Valid || key.String == "" {
		return nil
	}
	s := r.mediaURLString(ctx, key.String)
	return &s
}

func (r *Repo) mediaURLString(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	if r.r2 != nil {
		if u, err := r.r2.PresignedGetURL(ctx, key, 15*time.Minute); err == nil {
			return u
		}
	}
	return key
}

func displayNames(names []Name, scientific string) (string, *string) {
	var en, sw *string
	for i := range names {
		n := &names[i]
		if n.ReviewStatus != "reviewed" {
			continue
		}
		if n.Language == "en" && (en == nil || n.Preferred) {
			name := n.Name
			en = &name
		}
		if n.Language == "sw" && (sw == nil || n.Preferred) {
			name := n.Name
			sw = &name
		}
	}
	nameEn := scientific
	if en != nil {
		nameEn = *en
	}
	return nameEn, sw
}

func whereCopy(area, county, destination *string) string {
	target := ""
	switch {
	case area != nil && *area != "":
		target = *area
	case county != nil && *county != "":
		target = *county
	case destination != nil && *destination != "":
		target = strings.ReplaceAll(*destination, "-", " ")
	}
	if target == "" {
		return "Recorded in Kenya"
	}
	// Vocabulary from api/contracts/species.v1.yaml: never "guaranteed sighting".
	return "Recorded at " + target
}

func linkHref(targetType, targetID string) string {
	switch targetType {
	case "destination":
		return "/destinations/" + targetID
	case "course":
		return "/lms/courses/" + targetID
	case "audio_story":
		return "/stories/" + targetID
	case "event":
		return "/events/" + targetID
	}
	return ""
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func availableLanguages(cov []Coverage) []string {
	out := make([]string, 0, len(cov))
	for _, c := range cov {
		if c.Available {
			out = append(out, c.Language)
		}
	}
	return out
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return model.DefaultLimit
	case n > model.MaxLimit:
		return model.MaxLimit
	}
	return n
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func normaliseLanguage(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "en"
	}
	if len(v) > 20 {
		return "en"
	}
	return v
}
