package pagination

import (
	"encoding/json"
	"net/http"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CursorPaginator[T any] struct {
	Limit int32
}

func NewCursorPaginator[T any]() *CursorPaginator[T] {
	return &CursorPaginator[T]{}
}

type PageFunc[T any] func(limit int32) ([]T, error)
type PageAfterFunc[T any] func(limit int32, sortValue string, id string) ([]T, error)

func (p *CursorPaginator[T]) WritePage(w http.ResponseWriter, r *http.Request, first PageFunc[T], after PageAfterFunc[T], encodeCursor func(T) (string, bool)) {
	pr := ParseRequest(r)
	limit := int32(pr.Limit)

	var items []T
	var err error

	if pr.Cursor != nil {
		items, err = after(limit+1, pr.Cursor.SortValue, pr.Cursor.ID)
	} else {
		items, err = first(limit + 1)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list items")
		return
	}

	hasMore := len(items) > int(limit)
	result := items
	if hasMore {
		result = items[:limit]
	}

	resp := model.Paginated[T]{
		Items:   result,
		HasMore: hasMore,
	}
	if hasMore && len(result) > 0 {
		last := result[len(result)-1]
		if cursor, ok := encodeCursor(last); ok {
			resp.NextCursor = &cursor
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
