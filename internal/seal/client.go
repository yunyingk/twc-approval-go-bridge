package seal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

// Config identifies one Seal AI webhook channel. DocumentURL ends in /document;
// the attachment endpoint is the same channel's /attachments path.
type Config struct {
	DocumentURL string
	BearerToken string
}

type Client struct {
	documentURL   string
	attachmentURL string
	bearerToken   string
	httpClient    *http.Client
}

// NewClient does not make a network request. The caller owns the channel URL
// and Bearer token; neither is logged or included in API errors.
func NewClient(config Config, httpClient *http.Client) (*Client, error) {
	documentURL := strings.TrimSpace(config.DocumentURL)
	bearerToken := strings.TrimSpace(config.BearerToken)
	if bearerToken == "" {
		return nil, errors.New("Seal Bearer token is required")
	}
	u, err := url.Parse(documentURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/document") {
		return nil, errors.New("Seal document URL must be an absolute /document endpoint without credentials or query parameters")
	}
	attachmentURL := *u
	attachmentURL.Path = strings.TrimSuffix(u.Path, "/document") + "/attachments"
	attachmentURL.RawPath = ""
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	copyClient := *httpClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 60 * time.Second
	}
	// Never follow a redirect with the channel's Bearer credential.
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{documentURL: u.String(), attachmentURL: attachmentURL.String(), bearerToken: bearerToken, httpClient: &copyClient}, nil
}

type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// AttachmentInfo is the metadata expected in a document ATTACHMENT field.
type AttachmentInfo struct {
	Name         string `json:"name,omitempty"`
	MimeType     string `json:"mimeType,omitempty"`
	URL          string `json:"url,omitempty"`
	OSSPath      string `json:"ossPath,omitempty"`
	OSSSignedURL string `json:"ossSignedUrl,omitempty"`
	OSSFileSize  int64  `json:"ossFileSize,omitempty"`
}

type UploadedAttachment struct {
	AttachmentID string `json:"attachmentId"`
	AttachmentInfo
}

type UploadResponse struct {
	AttachmentID string
	Attachment   AttachmentInfo
	Attachments  []UploadedAttachment
}

// Valid means the upload response can be referenced by both an ATTACHMENT
// field and a structured invoice's evidenceAttachmentId.
func (r UploadResponse) Valid() bool {
	info := r.Attachment
	return strings.TrimSpace(r.AttachmentID) != "" && strings.TrimSpace(info.Name) != "" &&
		strings.TrimSpace(info.MimeType) != "" && strings.TrimSpace(info.URL) != "" &&
		strings.TrimSpace(info.OSSPath) != "" && strings.TrimSpace(info.OSSSignedURL) != "" &&
		info.OSSFileSize > 0
}

// UploadAttachment sends one already validated file as multipart field "files".
func (c *Client) UploadAttachment(ctx context.Context, file Attachment) (UploadResponse, error) {
	if c == nil {
		return UploadResponse{}, errors.New("Seal client is not initialized")
	}
	if len(file.Data) == 0 {
		return UploadResponse{}, errors.New("Seal attachment is empty")
	}
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = "attachment"
	}
	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": name})
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {disposition}, "Content-Type": {contentType}})
	if err != nil {
		return UploadResponse{}, errors.New("encode Seal attachment")
	}
	if _, err := part.Write(file.Data); err != nil {
		return UploadResponse{}, errors.New("encode Seal attachment")
	}
	if err := writer.Close(); err != nil {
		return UploadResponse{}, errors.New("encode Seal attachment")
	}
	raw, err := c.post(ctx, c.attachmentURL, writer.FormDataContentType(), body.Bytes(), "upload attachment")
	if err != nil {
		return UploadResponse{}, err
	}
	var response struct {
		Data struct {
			AttachmentID string         `json:"attachmentId"`
			Attachment   AttachmentInfo `json:"attachment"`
			AttachmentInfo
			Attachments []UploadedAttachment `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return UploadResponse{}, errors.New("decode Seal attachment response")
	}
	id := response.Data.AttachmentID
	if id == "" && len(response.Data.Attachments) > 0 {
		id = response.Data.Attachments[0].AttachmentID
	}
	if id == "" {
		return UploadResponse{}, errors.New("Seal attachment response has no attachment ID")
	}
	result := UploadResponse{AttachmentID: id, Attachments: response.Data.Attachments}
	if len(result.Attachments) > 0 {
		result.Attachment = result.Attachments[0].AttachmentInfo
	} else if response.Data.Attachment.Name != "" || response.Data.Attachment.URL != "" {
		result.Attachment = response.Data.Attachment
	} else {
		result.Attachment = response.Data.AttachmentInfo
	}
	if !result.Valid() {
		return UploadResponse{}, errors.New("Seal attachment upload returned an incomplete response")
	}
	return result, nil
}

// DocumentRequest is the external webhook's document and structured invoice
// payload. Amounts use json.Number so OCR values are sent as JSON numbers.
type DocumentRequest struct {
	DocumentID             string            `json:"documentId"`
	DocumentSN             string            `json:"documentSN"`
	DocumentURL            string            `json:"documentURL,omitempty"`
	StartTime              int64             `json:"startTime"`
	Fields                 []DocumentField   `json:"fields"`
	Invoices               []ExternalInvoice `json:"invoices,omitempty"`
	DisableAttachmentCache bool              `json:"disableAttachmentCache,omitempty"`
	ThinkingDepthOverride  string            `json:"thinkingDepthOverride,omitempty"`
}

type DocumentField struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Type         string `json:"type"`
	Value        any    `json:"value"`
	Comments     string `json:"comments,omitempty"`
	SemanticType string `json:"semanticType,omitempty"`
}

type InvoiceParty struct {
	Name string `json:"name"`
}

type ExternalInvoice struct {
	Kind                 string       `json:"kind"`
	SourceInvoiceID      string       `json:"sourceInvoiceId"`
	InvoiceType          string       `json:"invoiceType"`
	InvoiceNumber        string       `json:"invoiceNumber,omitempty"`
	CurrencyCode         string       `json:"currencyCode"`
	TotalAmount          json.Number  `json:"totalAmount"`
	Seller               InvoiceParty `json:"seller"`
	Buyer                InvoiceParty `json:"buyer"`
	EvidenceAttachmentID string       `json:"evidenceAttachmentId"`
	InvoiceDate          string       `json:"invoiceDate,omitempty"`
	TaxAmount            json.Number  `json:"taxAmount,omitempty"`
	AmountWithoutTax     json.Number  `json:"amountWithoutTax,omitempty"`
	ClaimedAmount        json.Number  `json:"claimedAmount,omitempty"`
}

type AcceptedInvoice struct {
	SourceInvoiceID string `json:"sourceInvoiceId"`
	Status          string `json:"status"`
}

type SubmitResponse struct {
	Success          bool
	DocumentID       string
	AcceptedInvoices []AcceptedInvoice
}

func (c *Client) SubmitDocument(ctx context.Context, document DocumentRequest) (SubmitResponse, error) {
	if c == nil {
		return SubmitResponse{}, errors.New("Seal client is not initialized")
	}
	if strings.TrimSpace(document.DocumentID) == "" || strings.TrimSpace(document.DocumentSN) == "" || document.StartTime <= 0 || document.Fields == nil {
		return SubmitResponse{}, errors.New("Seal document requires documentId, documentSN, startTime and fields")
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return SubmitResponse{}, errors.New("encode Seal document")
	}
	raw, err := c.post(ctx, c.documentURL, "application/json", payload, "submit document")
	if err != nil {
		return SubmitResponse{}, err
	}
	var response struct {
		Data struct {
			Success          *bool             `json:"success"`
			DocumentID       string            `json:"documentId"`
			AcceptedInvoices []AcceptedInvoice `json:"acceptedInvoices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return SubmitResponse{}, errors.New("decode Seal document response")
	}
	if response.Data.Success == nil || !*response.Data.Success {
		return SubmitResponse{}, errors.New("Seal did not accept document")
	}
	return SubmitResponse{Success: true, DocumentID: response.Data.DocumentID, AcceptedInvoices: response.Data.AcceptedInvoices}, nil
}

// HTTPStatusError exposes only the status code, not the response body, which
// can contain the credential or submitted receipt data.
type HTTPStatusError struct {
	Operation  string
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("Seal %s returned HTTP %d", e.Operation, e.StatusCode)
}

func (e *HTTPStatusError) HTTPStatus() int { return e.StatusCode }

func (c *Client) post(ctx context.Context, endpoint, contentType string, body []byte, operation string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Seal %s request: %w", operation, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Seal %s: %w", operation, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{Operation: operation, StatusCode: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Seal %s response: %w", operation, err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("Seal %s response exceeds size limit", operation)
	}
	return raw, nil
}
