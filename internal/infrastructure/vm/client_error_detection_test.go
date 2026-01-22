package vm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func TestQueryDetectsMissingTenantPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("cannot parse accountID from \"api\""))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	client := NewClient(domain.VMConnection{URL: srv.URL})
	_, err := client.Query(context.Background(), "vm_app_version", time.Now())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrMissingTenantPath) {
		t.Fatalf("expected ErrMissingTenantPath, got %v", err)
	}
}

func TestQueryDetectsUnsupportedURLFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("unsupported URL format for path \"/prometheus/api/v1/query\""))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[]}}`))
	}))
	defer srv.Close()

	client := NewClient(domain.VMConnection{URL: srv.URL})
	_, err := client.Query(context.Background(), "vm_app_version", time.Now())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrMissingTenantPath) {
		t.Fatalf("expected ErrMissingTenantPath, got %v", err)
	}
}
