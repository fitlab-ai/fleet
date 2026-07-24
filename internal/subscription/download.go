package subscription

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
)

const UserAgent = "clash.meta"
const MaxBytes = 10 * 1024 * 1024

type Downloader struct {
	Client  *http.Client
	Timeout time.Duration
}

func (d Downloader) Download(rawURL string) ([]byte, error) {
	value, err := ValidateURL(rawURL)
	if err != nil {
		return nil, err
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := d.Client
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" {
					return model.NewError("network", "Subscription redirect was rejected", nil)
				}
				return nil
			},
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, model.NewError("network", "Subscription download failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, model.NewError("http", fmt.Sprintf("Subscription server returned HTTP %d", resp.StatusCode), nil)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, model.NewError("network", "Subscription download failed", err)
	}
	if len(data) == 0 {
		return nil, model.NewError("http", "Subscription response was empty", nil)
	}
	if len(data) > MaxBytes {
		return nil, model.NewError("http", "Subscription response was too large", nil)
	}
	return data, nil
}

func HealthURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, model.NewError("health", "FLEET_HEALTH_URL must be a credential-free HTTPS URL", nil)
	}
	return parsed, nil
}
