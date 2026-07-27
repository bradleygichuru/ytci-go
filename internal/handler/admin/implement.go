package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// CourseCRUD adds course create/update endpoints (US 36)
type CourseCRUD struct {
	pool *pgxpool.Pool
}

func NewCourseCRUD(pool *pgxpool.Pool) *CourseCRUD {
	return &CourseCRUD{pool: pool}
}

func (h *CourseCRUD) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Difficulty  string `json:"difficulty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id, status string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO courses (title, description, difficulty, created_by) VALUES ($1, $2, $3, $4)
		 RETURNING id, status`, req.Title, req.Description, req.Difficulty, middleware.UserID(r.Context()),
	).Scan(&id, &status)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create course")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}

// QuizHandler handles quiz evaluation (US 35)
type QuizHandler struct {
	pool *pgxpool.Pool
}

func NewQuizHandler(pool *pgxpool.Pool) *QuizHandler {
	return &QuizHandler{pool: pool}
}

func (h *QuizHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CourseID string   `json:"courseId"`
		Answers  []answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())
	correct := 0
	total := len(req.Answers)

	for _, a := range req.Answers {
		if a.CorrectIndex == a.ChosenIndex {
			correct++
		}
		passThreshold := 70
		_ = passThreshold

		_ = userID
		_ = h.pool
	}

	passed := correct >= total*70/100

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"score":   fmt.Sprintf("%d/%d", correct, total),
		"passed":  passed,
		"correct": correct,
		"total":   total,
	})
}

type answer struct {
	QuestionIndex int `json:"questionIndex"`
	ChosenIndex   int `json:"chosenIndex"`
	CorrectIndex  int `json:"correctIndex"`
}

// ChallengeAdminCRUD handles admin challenge create (US 44)
type ChallengeAdminCRUD struct {
	pool *pgxpool.Pool
}

func NewChallengeAdminCRUD(pool *pgxpool.Pool) *ChallengeAdminCRUD {
	return &ChallengeAdminCRUD{pool: pool}
}

func (h *ChallengeAdminCRUD) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		BadgeName   string `json:"badgeName,omitempty"`
		StartDate   string `json:"startDate,omitempty"`
		EndDate     string `json:"endDate,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO challenges (title, description, badge_name, status, start_date, end_date, created_by)
		 VALUES ($1, $2, $3, 'draft', $4, $5, $6) RETURNING id`,
		req.Title, req.Description, req.BadgeName, req.StartDate, req.EndDate, middleware.UserID(r.Context()),
	).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create challenge")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}

// BulkImport handles CSV destination bulk upload (US 10)
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
		if i == 0 {
			continue
		}
		if len(row) < 3 {
			errors = append(errors, fmt.Sprintf("row %d: insufficient columns", i+1))
			continue
		}
		_, err := h.pool.Exec(r.Context(),
			`INSERT INTO destinations (name, slug, county, category, status)
			 VALUES ($1, $2, $3, $4, 'draft') ON CONFLICT DO NOTHING`,
			row[0], row[1], row[2], "attraction")
		if err != nil {
			errors = append(errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		imported++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"imported": imported,
		"errors":   errors,
	})
}

// UpdateAnalytics implements real DAU/WAU/MAU queries (US 47-48)
func UpdateAnalytics(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool) {
	var dau, wau, mau, registrations int

	today := time.Now().Truncate(24 * time.Hour)
	pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`, today).Scan(&dau)
	pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`,
		today.AddDate(0, 0, -7)).Scan(&wau)
	pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`,
		today.AddDate(0, -1, 0)).Scan(&mau)
	pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM users WHERE created_at >= $1`, today.AddDate(0, 0, -30)).Scan(&registrations)

	resp := map[string]any{
		"dau":              dau,
		"wau":              wau,
		"mau":              mau,
		"newRegistrations": registrations,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ItineraryStops handles itinerary stop management (US 21-22)
type ItineraryStops struct {
	pool *pgxpool.Pool
}

func NewItineraryStops(pool *pgxpool.Pool) *ItineraryStops {
	return &ItineraryStops{pool: pool}
}

type stopInput struct {
	DestinationID    string `json:"destinationId,omitempty"`
	Day              int    `json:"day"`
	DisplayOrder     int    `json:"displayOrder"`
	Title            string `json:"title,omitempty"`
	Description      string `json:"description,omitempty"`
	EstimatedCost    string `json:"estimatedCost,omitempty"`
}

func (h *ItineraryStops) UpsertStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	var req struct {
		Stops []stopInput `json:"stops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(), `DELETE FROM itinerary_stops WHERE itinerary_id = $1`, itineraryID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update stops")
		return
	}

	for _, s := range req.Stops {
		_, err = h.pool.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, day, display_order, title, description, estimated_cost)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			itineraryID, s.Day, s.DisplayOrder, s.Title, s.Description, s.EstimatedCost)
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to insert stop")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ItineraryStops) GetStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")

	rows, err := h.pool.Query(r.Context(),
		`SELECT day, display_order, COALESCE(title, ''), COALESCE(description, ''), COALESCE(estimated_cost, '')
		 FROM itinerary_stops WHERE itinerary_id = $1 ORDER BY day, display_order`, itineraryID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get stops")
		return
	}
	defer rows.Close()

	var stops []stopInput
	for rows.Next() {
		var s stopInput
		rows.Scan(&s.Day, &s.DisplayOrder, &s.Title, &s.Description, &s.EstimatedCost)
		stops = append(stops, s)
	}
	if stops == nil {
		stops = []stopInput{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stops": stops})
}
