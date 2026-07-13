package stadia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
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
	log          *logger.Logger
}

func NewHTTPClient(apiKey string) *HTTPClient {
	return &HTTPClient{
		apiKey:       apiKey,
		isochroneURL: defaultIsochroneURL,
		matrixURL:    defaultMatrixURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		log:          logger.Discard(),
	}
}

// NewHTTPClientWithBase constructs an HTTPClient with custom endpoint URLs for testing.
func NewHTTPClientWithBase(isoURL, matURL, apiKey string) *HTTPClient {
	return &HTTPClient{
		apiKey:       apiKey,
		isochroneURL: isoURL,
		matrixURL:    matURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		log:          logger.Discard(),
	}
}

// WithLogger attaches a logger and returns the client for chaining.
func (c *HTTPClient) WithLogger(l *logger.Logger) *HTTPClient {
	c.log = l
	return c
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
	if err := c.post(ctx, "isochrone", c.isochroneURL, body, &resp); err != nil {
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
	if err := c.post(ctx, "matrix", c.matrixURL, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type stadiaErrorBody struct {
	ErrorCode int    `json:"error_code"`
	Error     string `json:"error"`
}

func (c *HTTPClient) post(ctx context.Context, endpoint, url string, body any, out any) error {
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

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		c.log.Debugf("stadia %s: request error latency=%s err=%v", endpoint, latency, err)
		return fmt.Errorf("stadia: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.log.Debugf("stadia %s: status=%d latency=%s body=%q", endpoint, resp.StatusCode, latency, string(raw))
		var errBody stadiaErrorBody
		_ = json.Unmarshal(raw, &errBody)
		msg := errBody.Error
		if msg == "" {
			msg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		switch resp.StatusCode {
		case http.StatusBadRequest:
			return fmt.Errorf("%w: %s", ErrStadiaBadRequest, msg)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: %s", ErrStadiaRateLimit, msg)
		default:
			return fmt.Errorf("%w: status %d: %s", ErrStadiaUpstream, resp.StatusCode, msg)
		}
	}
	c.log.Debugf("stadia %s: status=%d latency=%s", endpoint, resp.StatusCode, latency)

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("stadia: decode response: %w", err)
	}
	return nil
}
