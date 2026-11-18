package alerts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type County struct {
	GID   int
	Name  string
	PCode string
}

type CountyMatch struct {
	County  County
	Method  string
	Overlap *float64
}

type AreaMapping struct {
	Counties  []CountyMatch
	Method    string
	Review    bool
	Ambiguous []string
	Unmatched []string
}

const (
	MethodPolygon  = "polygon"
	MethodAreaDesc = "area_desc"
	MethodRegion   = "region"
	MethodMixed    = "mixed"
	MethodNational = "national"
	MethodManual   = "manual"
	MethodUnmapped = "unmapped"
)

var (
	areaSepRE     = regexp.MustCompile(`[,;:/]`)
	andSepRE      = regexp.MustCompile(`(?i)\s+(?:and|&)\s+`)
	countyWordRE  = regexp.MustCompile(`\b(county|counties|region|regions)\b`)
	countryWordRE = regexp.MustCompile(`\b(of the country|country|kenya)\b`)
	nonNameRE     = regexp.MustCompile(`[^a-z0-9' -]`)
)

var countyAliases = map[string]string{
	"tanariver":       "Tana River",
	"taita taveta":    "Taita-Taveta",
	"taitataveta":     "Taita-Taveta",
	"transzoia":       "Trans Nzoia",
	"trans nzoia":     "Trans Nzoia",
	"uasin gishu":     "Uasin Gishu",
	"uasangishu":      "Uasin Gishu",
	"homabay":         "Homa Bay",
	"homa bay":        "Homa Bay",
	"tharakanithi":    "Tharaka-Nithi",
	"tharaka nithi":   "Tharaka-Nithi",
	"westpokot":       "West Pokot",
	"west pokot":      "West Pokot",
	"elgeyomarakwet":  "Elgeyo-Marakwet",
	"elgeyo marakwet": "Elgeyo-Marakwet",
	"muranga":         "Murang'a",
	"murang'a":        "Murang'a",
	"nairobi city":    "Nairobi",
}

var regionCounties = map[string][]string{
	"coast":         {"Mombasa", "Kwale", "Kilifi", "Tana River", "Lamu"},
	"coastal":       {"Mombasa", "Kwale", "Kilifi", "Tana River", "Lamu"},
	"western":       {"Kakamega", "Vihiga", "Bungoma", "Busia", "Trans Nzoia"},
	"nyanza":        {"Kisii", "Kisumu", "Homa Bay", "Migori", "Nyamira", "Siaya"},
	"central":       {"Kiambu", "Kirinyaga", "Murang'a", "Nyandarua", "Nyeri"},
	"eastern":       {"Embu", "Isiolo", "Kitui", "Machakos", "Makueni", "Marsabit", "Meru", "Tharaka-Nithi"},
	"northeastern":  {"Garissa", "Mandera", "Wajir"},
	"north eastern": {"Garissa", "Mandera", "Wajir"},
	"northern":      {"Garissa", "Isiolo", "Mandera", "Marsabit", "Wajir"},
	"rift valley": {"Baringo", "Bomet", "Elgeyo-Marakwet", "Kajiado", "Kericho", "Laikipia",
		"Nakuru", "Nandi", "Narok", "Samburu", "Trans Nzoia", "Turkana", "Uasin Gishu", "West Pokot"},
	"southeastern":      {"Kitui", "Machakos", "Makueni", "Taita-Taveta"},
	"south eastern":     {"Kitui", "Machakos", "Makueni", "Taita-Taveta"},
	"northwestern":      {"Turkana", "West Pokot", "Samburu"},
	"north western":     {"Turkana", "West Pokot", "Samburu"},
	"lake victoria":     {"Kisumu", "Siaya", "Homa Bay", "Migori", "Busia"},
	"central highlands": {"Kiambu", "Kirinyaga", "Murang'a", "Nyandarua", "Nyeri"},
}

var ignoredTokens = map[string]bool{
	"kenya": true, "country": true, "countrywide": true, "nationwide": true,
	"all": true, "several": true, "parts": true, "of": true, "the": true,
	"area": true, "areas": true, "highlands": true, "lowlands": true,
	"basin": true, "valley": true, "east": true, "west": true, "north": true,
	"south": true, "region": true, "regions": true, "county": true, "counties": true,
}

var ambiguousRegions = map[string]bool{"northern": true}

type CountyIndex struct {
	counties []County
	byName   map[string]County
}

func NewCountyIndex(counties []County) *CountyIndex {
	idx := &CountyIndex{counties: counties, byName: make(map[string]County, len(counties))}
	for _, c := range counties {
		for _, variant := range []string{c.Name, strings.ReplaceAll(c.Name, "-", " "), strings.ReplaceAll(c.Name, "'", "")} {
			idx.byName[normalizeName(variant)] = c
		}
	}
	return idx
}

func (i *CountyIndex) Empty() bool { return len(i.counties) == 0 }

func (i *CountyIndex) MatchAreaDesc(areaDesc string) AreaMapping {
	if i.Empty() {
		return AreaMapping{Method: MethodUnmapped, Review: true}
	}

	tokens := splitAreaDesc(areaDesc)
	seen := make(map[int]County)
	var exact, region []string
	var unmatched []string
	ambiguous := make([]string, 0)

	for _, token := range tokens {
		norm := normalizeName(token)
		if norm == "" || ignoredTokens[norm] {
			continue
		}
		if county, ok := i.lookup(norm); ok {
			seen[county.GID] = county
			exact = append(exact, county.Name)
			continue
		}
		if names, ok := regionCounties[norm]; ok {
			region = append(region, norm)
			if ambiguousRegions[norm] {
				ambiguous = append(ambiguous, norm)
			}
			for _, name := range names {
				if county, ok := i.lookup(normalizeName(name)); ok {
					seen[county.GID] = county
				}
			}
			continue
		}
		unmatched = append(unmatched, strings.TrimSpace(token))
	}

	method := MethodAreaDesc
	if len(exact) == 0 && len(region) > 0 {
		method = MethodRegion
	}
	if len(seen) == 0 {
		method = MethodUnmapped
	}
	review := len(ambiguous) > 0 || len(seen) == 0 || len(unmatched) > 0

	matches := make([]CountyMatch, 0, len(seen))
	for _, county := range seen {
		matches = append(matches, CountyMatch{County: county, Method: method})
	}
	sort.Slice(matches, func(a, b int) bool { return matches[a].County.GID < matches[b].County.GID })

	return AreaMapping{
		Counties:  matches,
		Method:    method,
		Review:    review,
		Ambiguous: uniqueStrings(ambiguous),
		Unmatched: uniqueStrings(unmatched),
	}
}

func (i *CountyIndex) lookup(norm string) (County, bool) {
	if c, ok := i.byName[norm]; ok {
		return c, true
	}
	if alias, ok := countyAliases[norm]; ok {
		c, found := i.byName[normalizeName(alias)]
		return c, found
	}
	return County{}, false
}

func splitAreaDesc(areaDesc string) []string {
	parts := areaSepRE.Split(areaDesc, -1)
	var out []string
	for _, part := range parts {
		for _, token := range andSepRE.Split(part, -1) {
			if strings.TrimSpace(token) != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("’", "'", "–", "-", "—", "-").Replace(value)
	value = countyWordRE.ReplaceAllString(value, " ")
	value = countryWordRE.ReplaceAllString(value, " ")
	value = nonNameRE.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// PolygonWKT converts a CAP polygon ("lat,lon lat,lon ...") to WKT (lon lat).
func PolygonWKT(points string) (string, error) {
	fields := strings.Fields(points)
	pairs := make([][2]float64, 0, len(fields))
	for _, field := range fields {
		parts := strings.Split(field, ",")
		if len(parts) != 2 {
			continue
		}
		var lat, lon float64
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%f", &lat); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &lon); err != nil {
			continue
		}
		pairs = append(pairs, [2]float64{lon, lat})
	}
	if len(pairs) < 3 {
		return "", fmt.Errorf("polygon needs at least 3 points")
	}
	if pairs[0] != pairs[len(pairs)-1] {
		pairs = append(pairs, pairs[0])
	}
	var b strings.Builder
	b.WriteString("POLYGON((")
	for i, pair := range pairs {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%.6f %.6f", pair[0], pair[1])
	}
	b.WriteString("))")
	return b.String(), nil
}
