package seal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	app "github.com/yunyingk/twc-approval-go-bridge/internal/app/review"
	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type receiver struct {
	calls int
	stale bool
}

func (r *receiver) Complete(_ context.Context, id, provider string, outcome core.Outcome) error {
	r.calls++
	if id != "id" || provider != "seal" || outcome.ExternalID != "approval" {
		panic("bad callback mapping")
	}
	if r.stale {
		return app.ErrStale
	}
	return nil
}
func TestCallbackSecretURLAndStaleAcknowledgement(t *testing.T) {
	rcv := &receiver{stale: true}
	mux := http.NewServeMux()
	mux.Handle("POST /seal/callback/{token}", CallbackHandler("secret", rcv))
	body := `{"documentId":"id","approvalRecordId":"approval","decision":"review"}`
	for _, item := range []struct {
		token  string
		status int
	}{{"wrong", 401}, {"secret", 200}} {
		r := httptest.NewRequest("POST", "/seal/callback/"+item.token, strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != item.status {
			t.Fatalf("code=%d", w.Code)
		}
	}
	if rcv.calls != 1 {
		t.Fatal("unauthenticated callback was applied")
	}
}
