package base

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/review"
)

type ReviewWriter struct {
	client      *LedgerClient
	base, table string
	fields      map[string]string
}

func NewReviewWriter(client *LedgerClient, base, table string, fields map[string]string) (*ReviewWriter, error) {
	if client == nil || base == "" || table == "" || fields["decision"] == "" || fields["document_id"] == "" || fields["revision"] == "" {
		return nil, fmt.Errorf("review writeback requires source table and decision, document_id, revision field IDs")
	}
	copyFields := make(map[string]string, len(fields))
	for k, v := range fields {
		copyFields[k] = v
	}
	return &ReviewWriter{client, base, table, copyFields}, nil
}

// WriteReviewResult updates explicitly configured audit fields. It does not
// overwrite human approval, reimbursement or settlement states.
func (w *ReviewWriter) WriteReviewResult(ctx context.Context, request core.Request, outcome core.Outcome) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	if request.LogicalID != "feishu:"+w.base+":"+w.table+":"+request.Document.RecordID {
		return fmt.Errorf("review result does not match configured source")
	}
	token, err := w.client.accessToken(ctx)
	if err != nil {
		return err
	}
	names, err := w.client.fieldNames(ctx, token, w.base, w.table)
	if err != nil {
		return err
	}
	values := map[string]string{"decision": outcome.Decision, "comment": outcome.Comment, "document_id": request.Document.DocumentID, "revision": request.Revision, "provider": request.Provider, "external_id": outcome.ExternalID, "url": outcome.URL}
	fields := make(map[string]any, len(w.fields))
	for semantic, id := range w.fields {
		value, ok := values[semantic]
		if !ok {
			return fmt.Errorf("unsupported review field %s", semantic)
		}
		name := names[id]
		if name == "" {
			return fmt.Errorf("configured review result field %s is missing", semantic)
		}
		fields[name] = value
	}
	endpoint := fmt.Sprintf("%s/bitable/v1/apps/%s/tables/%s/records/%s", feishuAPI, url.PathEscape(w.base), url.PathEscape(w.table), url.PathEscape(request.Document.RecordID))
	return w.client.request(ctx, http.MethodPut, endpoint, token, map[string]any{"fields": fields}, &struct{}{})
}
