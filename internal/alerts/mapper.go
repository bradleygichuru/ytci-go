package alerts

import (
	"context"
	"fmt"
	"strings"
)

type Mapper struct {
	store *Store
	index *CountyIndex
}

func NewMapper(ctx context.Context, store *Store) (*Mapper, error) {
	counties, err := store.Counties(ctx)
	if err != nil {
		return nil, err
	}
	if len(counties) == 0 {
		return nil, ErrCountiesEmpty
	}
	return &Mapper{store: store, index: NewCountyIndex(counties)}, nil
}

// MapInfo applies exact areaDesc county tokens first, then regions, then
// polygon intersection. Exact county lists are authoritative; regional text is
// unioned with polygons because neither source is complete on its own.
func (m *Mapper) MapInfo(ctx context.Context, info CAPInfo) (AreaMapping, error) {
	var descs []string
	for _, area := range info.Areas {
		if strings.TrimSpace(area.AreaDesc) != "" {
			descs = append(descs, area.AreaDesc)
		}
	}
	text := m.index.MatchAreaDesc(strings.Join(descs, ", "))

	var polygonMatches []CountyMatch
	for _, area := range info.Areas {
		for _, polygon := range area.Polygons {
			wkt, err := PolygonWKT(polygon)
			if err != nil {
				continue
			}
			matches, err := m.store.CountiesForPolygon(ctx, wkt)
			if err != nil {
				return AreaMapping{}, fmt.Errorf("map CAP polygon: %w", err)
			}
			polygonMatches = append(polygonMatches, matches...)
		}
	}

	if len(text.Counties) > 0 && text.Method == MethodAreaDesc && !text.Review {
		return text, nil
	}

	merged := make(map[int]CountyMatch)
	for _, m := range text.Counties {
		merged[m.County.GID] = m
	}
	for _, m := range polygonMatches {
		if _, ok := merged[m.County.GID]; !ok {
			merged[m.County.GID] = m
		}
	}

	method := text.Method
	switch {
	case len(text.Counties) > 0 && len(polygonMatches) > 0:
		method = MethodMixed
	case len(text.Counties) == 0 && len(polygonMatches) > 0:
		method = MethodPolygon
	}
	if len(merged) == 0 {
		method = MethodUnmapped
	}
	review := text.Review || len(merged) == 0

	out := make([]CountyMatch, 0, len(merged))
	for _, m := range merged {
		out = append(out, m)
	}
	return AreaMapping{
		Counties:  out,
		Method:    method,
		Review:    review,
		Ambiguous: text.Ambiguous,
		Unmatched: text.Unmatched,
	}, nil
}
