package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	core "github.com/yunyingk/twc-approval-go-bridge/internal/core/approval"
)

func nativeUploadRequest(t *testing.T, scope string) (core.UploadRequest, []byte) {
	t.Helper()
	data := []byte("%PDF-1.4\nPRIVATE_FILE_BODY")
	hash := sha256.Sum256(data)
	request, err := core.NewUploadRequest(core.UploadRequest{SourceScope: "source", SourceIdentity: "source-app", RecordID: "record", FieldID: "files-field", SourceFileID: "base-token", TargetScope: scope, ContentHash: hex.EncodeToString(hash[:]), ContentType: "application/pdf", Size: int64(len(data)), Name: "发票账单.pdf", Kind: "attachment"})
	if err != nil {
		t.Fatal(err)
	}
	return request, data
}

func TestApprovalUploadUsesLegacyMultipartAndSelectedAppIdentity(t *testing.T) {
	posts, auths := 0, 0
	client, err := NewFileUploader(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "open.feishu.cn" && strings.Contains(req.URL.Path, "tenant_access_token") {
			auths++
			var body map[string]string
			if json.NewDecoder(req.Body).Decode(&body) != nil || body["app_id"] != t.Name() || body["app_secret"] != "secret" {
				t.Fatal("upload used the wrong credentials")
			}
			return definitionResponse(`{"code":0,"tenant_access_token":"selected-token"}`), nil
		}
		if req.URL.String() != approvalUploadURL || req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer selected-token" {
			t.Fatal("upload did not follow documented endpoint/auth")
		}
		posts++
		if req.ParseMultipartForm(1<<20) != nil || req.FormValue("name") != "发票账单.pdf" || req.FormValue("type") != "attachment" {
			t.Fatal("filename or form purpose was lost")
		}
		file, header, err := req.FormFile("content")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "发票账单.pdf" || string(data) != "%PDF-1.4\nPRIVATE_FILE_BODY" {
			t.Fatal("multipart changed original content/name")
		}
		return definitionResponse(`{"code":0,"data":{"code":"approval-code","url":"PRIVATE_SIGNED_URL"}}`), nil
	})})
	if err != nil || posts != 0 || auths != 0 {
		t.Fatal("constructor called provider")
	}
	request, data := nativeUploadRequest(t, client.TargetScope())
	ref, err := client.Upload(context.Background(), request, data)
	if err != nil || ref.Scope != client.TargetScope() || ref.ID != "approval-code" || posts != 1 || auths != 1 {
		t.Fatalf("upload did not return scoped file code: %v", err)
	}
	raw, _ := json.Marshal(ref)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "base-token") {
		t.Fatal("signed URL or Base token escaped as a file code")
	}
}

func TestApprovalUploadUnknownErrorsNeverRetryOrExposeProviderBody(t *testing.T) {
	for _, scenario := range []string{"lost", "invalid envelope", "missing code", "internal", "contradictory status", "permission", "permission on 429", "permission on 500", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			original := &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "tenant_access_token") {
					return definitionResponse(`{"code":0,"tenant_access_token":"token"}`), nil
				}
				posts++
				switch scenario {
				case "lost":
					return nil, errors.New("PRIVATE_RESPONSE_LOST")
				case "invalid envelope":
					return definitionResponse(`{"data":{"code":"unproven"}}`), nil
				case "missing code":
					return definitionResponse(`{"code":0,"data":{"url":"PRIVATE_URL"}}`), nil
				case "internal":
					return definitionResponse(`{"code":1395001,"msg":"PRIVATE_ERROR"}`), nil
				}
				status := http.StatusOK
				body := `{"code":99991672,"msg":"PRIVATE_SCOPE"}`
				switch scenario {
				case "permission":
					status = 403
				case "permission on 429":
					status = 429
				case "permission on 500":
					status = 500
				case "contradictory status":
					status = 500
					body = `{"code":0,"data":{"code":"unproven"}}`
				case "redirect":
					status = 302
					body = `{"code":0,"data":{"code":"unproven"}}`
				}
				response := definitionResponse(body)
				response.StatusCode = status
				response.Header.Set("Location", "https://PRIVATE_OTHER_HOST.invalid/upload")
				return response, nil
			})}
			client, err := NewFileUploader(t.Name(), "secret", original)
			if err != nil {
				t.Fatal(err)
			}
			if original.CheckRedirect != nil {
				t.Fatal("constructor mutated caller HTTP client")
			}
			request, data := nativeUploadRequest(t, client.TargetScope())
			_, err = client.Upload(context.Background(), request, data)
			if err == nil || strings.Contains(err.Error(), "PRIVATE") || posts != 1 {
				t.Fatal("upload retried or exposed unknown protocol content")
			}
			if errors.Is(err, core.ErrUploadRejected) != (scenario == "permission") {
				t.Fatal("uncertain response became a proven rejection")
			}
		})
	}
}

func TestApprovalUploadRejectsPayloadMismatchBeforeAuthentication(t *testing.T) {
	calls := 0
	client, _ := NewFileUploader(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })})
	for _, scenario := range []string{"different bytes", "different scope", "wrong purpose", "wrong MIME", "invalid name"} {
		request, data := nativeUploadRequest(t, client.TargetScope())
		switch scenario {
		case "different bytes":
			data = []byte("different body")
		case "different scope":
			request.TargetScope = "another-app"
		case "wrong purpose":
			request.Kind = "image"
		case "wrong MIME":
			request.ContentType = "image/jpeg"
		case "invalid name":
			request.Name = "../../invoice.pdf"
		}
		request.ID = ""
		request, err := core.NewUploadRequest(request)
		if err == nil {
			_, err = client.Upload(context.Background(), request, data)
		}
		if err == nil {
			t.Fatalf("invalid payload accepted: %s", scenario)
		}
	}
	if calls != 0 {
		t.Fatal("invalid content reached authentication/upload")
	}
}

func TestApprovalAuthFailureIsRejectedBeforeFilePOST(t *testing.T) {
	posts := 0
	client, _ := NewFileUploader(t.Name(), "secret", &http.Client{Transport: definitionTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == approvalUploadURL {
			posts++
		}
		return nil, errors.New("PRIVATE_AUTH_TRANSPORT")
	})})
	request, data := nativeUploadRequest(t, client.TargetScope())
	_, err := client.Upload(context.Background(), request, data)
	if !errors.Is(err, core.ErrUploadRejected) || posts != 0 || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("auth failure lost proof that file POST did not happen")
	}
}
