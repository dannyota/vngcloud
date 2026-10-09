package cdn

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// Certificate is one vCDN certificate. The server also returns the private
// key, the owner, and the deletion fields; the model has no field for them,
// so a decode never holds the key. Dates stay the server's strings, such as
// "28 Apr 2025 06:53:20 GMT". Certificate and CARoot hold the PEM text and
// are set by GetCertificate only.
type Certificate struct {
	ID          string `json:"cdnSslcertificateId"`
	CommonName  string `json:"commonName"`
	CName       string `json:"cname"`
	Issuer      string `json:"issuer"`
	ValidFrom   string `json:"validFrom"`
	ExpiresOn   string `json:"expiresOn"`
	Status      int    `json:"status"`
	CDNUsing    int    `json:"cdnUsing"`
	CreatedTime string `json:"createdTime"`
	Certificate string `json:"certificate,omitempty"`
	CARoot      string `json:"caRoot,omitempty"`
}

// ListCertificatesInput has no fields today; a nil Input is valid.
type ListCertificatesInput struct{}

type ListCertificatesOutput = core.List[Certificate]

// ListCertificates lists every certificate of the account in one call. The
// API has no paging, and an account with none gives empty Items. It needs
// an API key (ErrNoAPIKey otherwise).
//
// The server sends every certificate's private key in this answer. The
// request is marked sensitive, so the capture hook never sees the body, and
// the model drops the key.
func (c *Client) ListCertificates(ctx context.Context, in *ListCertificatesInput) (*ListCertificatesOutput, error) {
	const op = "cdn.ListCertificates"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	r := call{op: op, method: http.MethodGet, parts: []string{"certificate", "list"}, sensitive: true}
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	items, err := decodeList[Certificate](r, data, true)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Certificate, items[i].CARoot = "", ""
	}
	return &ListCertificatesOutput{Items: items}, nil
}

// GetCertificateInput identifies the certificate to read.
type GetCertificateInput struct {
	CertificateID string `vngcloud:"required"`
}

type GetCertificateOutput struct {
	Certificate Certificate
}

// GetCertificate reads one certificate, with its PEM text. An unknown ID
// gives an error that matches ErrNotFound. Like ListCertificates, it drops
// the private key the server returns.
func (c *Client) GetCertificate(ctx context.Context, in *GetCertificateInput) (*GetCertificateOutput, error) {
	const op = "cdn.GetCertificate"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CertificateID", in.CertificateID); err != nil {
		return nil, err
	}
	r := call{
		op: op, method: http.MethodGet, parts: []string{"certificate", "detail", in.CertificateID},
		sensitive: true, notFound: "no such certificate",
	}
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	var cert Certificate
	if emptyData(data) || decodeObject(data, &cert) != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	return &GetCertificateOutput{Certificate: cert}, nil
}
