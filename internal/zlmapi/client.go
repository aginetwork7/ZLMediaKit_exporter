// Package zlmapi is a thin client for the ZLMediaKit HTTP API.
//
// Every endpoint replies with the same envelope: a "code" field that is zero on
// success, a human readable "msg", and an endpoint specific "data" payload.
package zlmapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// SuccessCode is the value ZLMediaKit puts in "code" when a call succeeded.
const SuccessCode = 0

// Endpoints scraped by the exporter.
const (
	EndpointVersion            = "index/api/version"
	EndpointGetAPIList         = "index/api/getApiList"
	EndpointGetThreadsLoad     = "index/api/getThreadsLoad"
	EndpointGetWorkThreadsLoad = "index/api/getWorkThreadsLoad"
	EndpointGetStatistic       = "index/api/getStatistic"
	EndpointGetAllSession      = "index/api/getAllSession"
	EndpointGetMediaList       = "index/api/getMediaList"
	EndpointListRtpServer      = "index/api/listRtpServer"

	EndpointGetWebRtcConnectionList = "index/api/getWebRtcConnectionList"
	EndpointWhipBatchInfo           = "index/api/whipBatchInfo"
)

// Options tunes the HTTP transport used to reach ZLMediaKit.
type Options struct {
	// SSLVerify mirrors the historical --web.ssl-verify flag.
	SSLVerify bool
}

// Client talks to a single ZLMediaKit API server.
type Client struct {
	baseURL string
	secret  string
	http    http.Client
}

// NewClient returns a client for the ZLMediaKit API server at baseURL.
func NewClient(baseURL, secret string, opts Options) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("ZlMediaKit API uri is required")
	}
	if secret == "" {
		return nil, errors.New("ZlMediaKit API secret is required")
	}

	c := &Client{baseURL: baseURL, secret: secret}
	if opts.SSLVerify {
		c.http.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // preserved semantics, fixed separately
		}
	}
	return c, nil
}

// Response is the envelope every ZLMediaKit API endpoint replies with.
type Response[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

// PagedResponse is the envelope of paginated endpoints, which report the
// total item count next to "data".
type PagedResponse[T any] struct {
	Code  int    `json:"code"`
	Msg   string `json:"msg"`
	Total int    `json:"total"`
	Data  T      `json:"data"`
}

// APIError reports a ZLMediaKit response carrying a non-zero code.
type APIError struct {
	Endpoint string
	Code     int
	Msg      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("unexpected API response code from %s: %d, reason: %s", e.Endpoint, e.Code, e.Msg)
}

// Get fetches endpoint and returns the decoded "data" payload.
func Get[T any](ctx context.Context, c *Client, endpoint string) (T, error) {
	return GetQuery[T](ctx, c, endpoint, nil)
}

// GetQuery is Get with query parameters.
func GetQuery[T any](ctx context.Context, c *Client, endpoint string, query url.Values) (T, error) {
	var zero T
	var resp Response[T]
	if err := c.call(ctx, endpoint, query, &resp); err != nil {
		return zero, err
	}
	if resp.Code != SuccessCode {
		return zero, &APIError{Endpoint: endpoint, Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data, nil
}

// GetPage fetches a paginated endpoint and returns the "data" payload along
// with the reported total.
func GetPage[T any](ctx context.Context, c *Client, endpoint string, query url.Values) (T, int, error) {
	var zero T
	var resp PagedResponse[T]
	if err := c.call(ctx, endpoint, query, &resp); err != nil {
		return zero, 0, err
	}
	if resp.Code != SuccessCode {
		return zero, 0, &APIError{Endpoint: endpoint, Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data, resp.Total, nil
}

func (c *Client) call(ctx context.Context, endpoint string, query url.Values, out any) error {
	uri := fmt.Sprintf("%s/%s", c.baseURL, endpoint)
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("error parsing URL %q: %w", uri, err)
	}
	if len(query) > 0 {
		parsedURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("error building request for %s: %w", endpoint, err)
	}
	req.Header.Set("secret", c.secret)

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("error scraping ZLMediaKit: %w", err)
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("error decoding JSON response from %s: %w", endpoint, err)
	}
	return nil
}
