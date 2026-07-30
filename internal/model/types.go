package model

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrorResponse struct {
	Errors []ValidationError `json:"errors"`
}

type Paginated[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

type PaginationParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  int     `json:"limit,omitempty"`
}

const (
	DefaultLimit = 50
	MaxLimit     = 100
)
