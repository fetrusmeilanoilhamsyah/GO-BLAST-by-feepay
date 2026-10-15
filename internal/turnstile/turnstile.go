package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var (
	ErrTokenMissing     = errors.New("token turnstile tidak ditemukan")
	ErrVerificationFail = errors.New("verifikasi turnstile gagal")
	ErrActionMismatch   = errors.New("action turnstile tidak sesuai")
	ErrHostnameMismatch = errors.New("hostname turnstile tidak sesuai")
	ErrServiceOutage    = errors.New("layanan turnstile tidak terjangkau")
)

type VerifyResponse struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
	Action      string    `json:"action"`
	CData       string    `json:"cdata"`
}

type Client struct {
	SiteKey    string
	SecretKey  string
	PublicHost string
	VerifyURL  string
	HTTPClient *http.Client
}

func NewClient(siteKey, secretKey, publicHost string) *Client {
	return &Client{
		SiteKey:    siteKey,
		SecretKey:  secretKey,
		PublicHost: publicHost,
		VerifyURL:  DefaultVerifyURL,
		HTTPClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// IsEnabled memeriksa apakah Turnstile dikonfigurasi dan aktif.
func (c *Client) IsEnabled() bool {
	return c != nil && c.SiteKey != "" && c.SecretKey != ""
}

// Verify memvalidasi token cf-turnstile-response ke endpoint siteverify Cloudflare.
// Mengembalikan (success bool, err error).
// Jika server Cloudflare tidak terjangkau atau timeout, mengembalikan (false, ErrServiceOutage).
func (c *Client) Verify(ctx context.Context, responseToken, expectedAction, remoteIP string) (bool, error) {
	if !c.IsEnabled() {
		return true, nil
	}

	responseToken = strings.TrimSpace(responseToken)
	if responseToken == "" {
		return false, ErrTokenMissing
	}

	form := url.Values{}
	form.Set("secret", c.SecretKey)
	form.Set("response", responseToken)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	verifyURL := c.VerifyURL
	if verifyURL == "" {
		verifyURL = DefaultVerifyURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Gangguan jaringan, timeout, atau host tidak terjangkau
		return false, ErrServiceOutage
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, ErrServiceOutage
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return false, ErrServiceOutage
	}

	var res VerifyResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return false, ErrServiceOutage
	}

	if !res.Success {
		return false, ErrVerificationFail
	}

	// Validasi action jika expectedAction ditentukan
	if expectedAction != "" && res.Action != "" && res.Action != expectedAction {
		return false, ErrActionMismatch
	}

	// Validasi hostname jika PublicHost ditentukan
	if c.PublicHost != "" && res.Hostname != "" {
		if !strings.EqualFold(res.Hostname, c.PublicHost) {
			return false, ErrHostnameMismatch
		}
	}

	return true, nil
}
