package request

import (
	"encoding/json"
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
)

// JSON decodes a JSON request body into dst.
//
// Request decoding is an HTTP boundary concern. Handlers may use this helper,
// but services should receive already-validated DTOs and should not depend on
// net/http.
func JSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}

	return true
}
