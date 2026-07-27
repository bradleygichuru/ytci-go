package pagination

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type Cursor struct {
	SortValue string
	ID        string
}

func EncodeCursor(sortValue, id string) string {
	raw := fmt.Sprintf("%s|%s", sortValue, id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func DecodeCursor(encoded string) (Cursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Cursor{}, false
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return Cursor{}, false
	}
	return Cursor{SortValue: parts[0], ID: parts[1]}, true
}

func UUIDString(bytes [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

type PageRequest struct {
	Limit  int
	Cursor *Cursor
}

func ParseRequest(r *http.Request) PageRequest {
	limit := model.DefaultLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
			if limit > model.MaxLimit {
				limit = model.MaxLimit
			}
		}
	}
	pr := PageRequest{Limit: limit}
	if encoded := r.URL.Query().Get("cursor"); encoded != "" {
		if cur, ok := DecodeCursor(encoded); ok {
			pr.Cursor = &cur
		}
	}
	return pr
}
