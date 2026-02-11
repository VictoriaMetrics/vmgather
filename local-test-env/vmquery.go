package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRequestTimeout = 10 * time.Second
	freshnessWindow       = 120 * time.Second
)

type vmQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func buildVMQueryURL(base string, query string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", base, err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query"
	qv := u.Query()
	qv.Set("query", query)
	u.RawQuery = qv.Encode()
	return u.String(), nil
}

func doVMQuery(ctx context.Context, httpClient *http.Client, endpoint string, auth *AuthConfig, query string) (*vmQueryResponse, []byte, error) {
	fullURL, err := buildVMQueryURL(endpoint, query)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, nil, err
	}
	applyAuth(req, auth)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, body, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}

	var parsed vmQueryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, body, fmt.Errorf("invalid json: %w", err)
	}
	if parsed.Status != "success" {
		return &parsed, body, fmt.Errorf("query status=%q", parsed.Status)
	}
	return &parsed, body, nil
}

func applyAuth(req *http.Request, auth *AuthConfig) {
	if auth == nil || auth.Type == "" || auth.Type == "none" {
		return
	}
	switch auth.Type {
	case "basic":
		req.SetBasicAuth(auth.Username, auth.Password)
	case "bearer":
		if auth.Token != "" {
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}
	}
}

func extractVMQueryTimestampSeconds(resp *vmQueryResponse) (int64, bool) {
	if resp == nil || len(resp.Data.Result) == 0 {
		return 0, false
	}
	v := resp.Data.Result[0].Value
	if len(v) < 1 {
		return 0, false
	}
	switch t := v[0].(type) {
	case float64:
		return int64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return int64(f), true
	default:
		return 0, false
	}
}

func isFreshTimestamp(tsSeconds int64, now time.Time) bool {
	if tsSeconds <= 0 {
		return false
	}
	tsTime := time.Unix(tsSeconds, 0)
	age := now.Sub(tsTime)
	if age < 0 {
		age = -age
	}
	return age < freshnessWindow
}
