package approval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

const approvalUploadURL = "https://www.feishu.cn/approval/openapi/v2/file/upload"

// FileUploader uses the documented legacy multipart endpoint, with the selected
// target application's tenant token. It never creates instances or retries POST.
type FileUploader struct {
	appID, secret, scope string
	http                 *http.Client
}

func NewFileUploader(appID, secret string, client *http.Client) (*FileUploader, error) {
	appID, secret = strings.TrimSpace(appID), strings.TrimSpace(secret)
	if appID == "" || secret == "" {
		return nil, fmt.Errorf("approval upload requires explicit application credentials")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	copy := *client
	// A redirect must not forward an authenticated body to another host or path.
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &FileUploader{appID, secret, "feishu-app:" + appID, &copy}, nil
}
func (c *FileUploader) TargetScope() string { return c.scope }

type uploadEnvelope struct {
	Code  *int   `json:"code"`
	Token string `json:"tenant_access_token"`
	Data  struct {
		Code string `json:"code"`
	} `json:"data"`
}

func (c *FileUploader) call(ctx context.Context, endpoint, contentType, token string, body io.Reader, operation string) (uploadEnvelope, error) {
	var envelope uploadEnvelope
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return envelope, &instanceTransportError{operation, err}
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return envelope, &instanceTransportError{operation, err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
		return envelope, &instanceTransportError{operation: operation, cause: err}
	}
	if *envelope.Code != 0 {
		return envelope, &InstanceAPIError{operation, resp.StatusCode, *envelope.Code}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return envelope, &InstanceAPIError{operation, resp.StatusCode, 0}
	}
	return envelope, nil
}

func (c *FileUploader) Upload(ctx context.Context, request core.UploadRequest, data []byte) (core.Identity, error) {
	if c == nil || c.http == nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, fmt.Errorf("approval uploader is not initialized"))
	}
	if err := request.Validate(); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	hash := sha256.Sum256(data)
	mediaType := strings.Split(http.DetectContentType(data), ";")[0]
	if request.TargetScope != c.scope || request.Size != int64(len(data)) || request.ContentHash != hex.EncodeToString(hash[:]) || request.ContentType != mediaType || (request.Kind == "image" && !strings.HasPrefix(mediaType, "image/")) {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, fmt.Errorf("approval upload content or target does not match its frozen request"))
	}
	if err := ctx.Err(); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	auth, _ := json.Marshal(map[string]string{"app_id": c.appID, "app_secret": c.secret})
	envelope, err := c.call(ctx, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", "application/json", "", bytes.NewReader(auth), "upload_auth")
	if err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	} // File POST has not happened.
	if envelope.Token == "" {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, &instanceTransportError{operation: "upload_auth"})
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("name", request.Name); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	if err := form.WriteField("type", request.Kind); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	part, err := form.CreateFormFile("content", request.Name)
	if err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	if _, err = part.Write(data); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	if err = form.Close(); err != nil {
		return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
	}
	envelope, err = c.call(ctx, approvalUploadURL, form.FormDataContentType(), envelope.Token, &body, "upload")
	if err != nil {
		var api *InstanceAPIError
		if errors.As(err, &api) && ((api.StatusCode >= 200 && api.StatusCode < 300) || api.StatusCode == 400 || api.StatusCode == 401 || api.StatusCode == 403) {
			// Documented gateway authentication/permission failures prove rejection.
			switch api.Code {
			case 99991401, 99991661, 99991663, 99991665, 99991672:
				return core.Identity{}, errors.Join(core.ErrUploadRejected, err)
			}
		}
		return core.Identity{}, err
	}
	if envelope.Data.Code == "" || envelope.Data.Code != strings.TrimSpace(envelope.Data.Code) {
		return core.Identity{}, &instanceTransportError{operation: "upload"}
	}
	// The expiring URL is deliberately ignored; only the file code is persisted.
	return core.Identity{Scope: c.scope, ID: envelope.Data.Code}, nil
}
