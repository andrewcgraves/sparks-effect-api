package stadia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultIsochroneURL = "https://api.stadiamaps.com/isochrone/v1"
	defaultMatrixURL    = "https://api.stadiamaps.com/matrix/v1"
)

// HTTPClient is the live Stadia adapter. The API key is never logged.
type HTTPClient struct {
	apiKey       string
	isochroneURL string
	matrixURL    string
	httpClient   *http.Client
}

func NewHTTPClient(apiKey string) *HTTPClient {
	return &HTTPClient{
		apiKey:       apiKey,
		isochroneURL: defaultIsochroneURL,
		matrixURL:    defaultMatrixURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// NewHTTPClientWithBase constructs an HTTPClient with custom endpoint URLs for testing.
func NewHTTPClientWithBase(isoURL, matURL, apiKey string) *HTTPClient {
	return &HTTPClient{
		apiKey:       apiKey,
		isochroneURL: isoURL,
		matrixURL:    matURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPClient) Isochrone(ctx context.Context, req IsochroneRequest) (*IsochroneResponse, error) {
	body := map[string]any{
		"locations": []map[string]any{
			{"lat": req.Origin.Lat, "lon": req.Origin.Lng},
		},
		"costing": req.Costing,
		"contours": []map[string]any{
			{"time": req.BudgetSecs / 60},
		},
		"polygons": true,
	}
	var resp IsochroneResponse
	if err := c.post(ctx, c.isochroneURL, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) Matrix(ctx context.Context, req MatrixRequest) (*MatrixResponse, error) {
	sources := make([]map[string]any, len(req.Origins))
	for i, o := range req.Origins {
		sources[i] = map[string]any{"lat": o.Lat, "lon": o.Lng}
	}
	targets := make([]map[string]any, len(req.Destinations))
	for i, d := range req.Destinations {
		targets[i] = map[string]any{"lat": d.Lat, "lon": d.Lng}
	}
	body := map[string]any{
		"sources": sources,
		"targets": targets,
		"costing": req.Costing,
	}
	var resp MatrixResponse
	if err := c.post(ctx, c.matrixURL, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) post(ctx context.Context, url string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("stadia: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("stadia: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Stadia-Auth "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("stadia: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stadia: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("stadia: decode response: %w", err)
	}
	return nil
}
