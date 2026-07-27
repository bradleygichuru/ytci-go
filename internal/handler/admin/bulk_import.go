package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

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
		_, err := h.pool.Exec(r.Context(),
			`INSERT INTO destinations (name, slug, county, category, status)
			 VALUES ($1, $2, $3, 'attraction', 'draft') ON CONFLICT DO NOTHING`,
			row[0], row[1], row[2])
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
