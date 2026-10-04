package seal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type ResultReceiver interface {
	Complete(context.Context, string, string, core.Outcome) error
}

// CallbackHandler uses an integration-owned secret URL, not an assumed Seal
// signature header. Configure that exact HTTPS URL in Seal's callback setting.
// The token must not appear in access logs at the application or ingress.
func CallbackHandler(token string, receiver ResultReceiver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResponseBytes))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id, outcome, err := DecodeCallback(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := receiver.Complete(r.Context(), id, "seal", outcome); err != nil && !errors.Is(err, app.ErrStale) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})
}
