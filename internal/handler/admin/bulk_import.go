package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type BulkImport struct {
	pool *pgxpool.Pool
}

func NewBulkImport(pool *pgxpool.Pool) *BulkImport {
	return &BulkImport{pool: pool}
}

func (h *BulkImport) Import(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_FILE", "file is required")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CSV", "failed to parse CSV")
		return
	}

	imported := 0
	var errors []string
	for i, row := range records {
		if i == 0 || len(row) < 3 {
			continue
		}
		categoryValue := "Sightseeing"
		if len(row) >= 4 && strings.TrimSpace(row[3]) != "" {
			categoryValue = row[3]
		}
		category, ok := catalog.LegacyCategory(categoryValue)
		if !ok {
			errors = append(errors, fmt.Sprintf("row %d: unsupported category %q", i+1, categoryValue))
			continue
		}
		_, err := h.pool.Exec(r.Context(),
			`INSERT INTO destinations (name, slug, county, category, status)
			 VALUES ($1, $2, $3, $4, 'draft') ON CONFLICT DO NOTHING`,
			row[0], row[1], row[2], string(category))
		if err != nil {
			errors = append(errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		imported++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"imported": imported, "errors": errors,
	})
}
