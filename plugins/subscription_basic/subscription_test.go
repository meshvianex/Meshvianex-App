package subscription_basic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meshvianex/core/resources"
)

func TestFetchRawAndHTTPResponses(t *testing.T) {
	fetcher := Instance{}
	raw, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: "direct://", SourceType: "raw"})
	if err != nil || raw != "direct://" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("direct://"))
	}))
	defer server.Close()
	got, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: server.URL + "/feed", SourceType: "url"})
	if err != nil || got != "direct://" {
		t.Fatalf("HTTP result=%q err=%v", got, err)
	}
	if _, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: server.URL + "/missing", SourceType: "url"}); err == nil {
		t.Fatal("non-2xx response unexpectedly succeeded")
	}
}

func TestFetchRedirectAndBodyLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte(strings.Repeat("x", int(maxSubscriptionBytes)+1)))
			return
		}
		n := 0
		_, _ = fmt.Sscanf(r.URL.Query().Get("n"), "%d", &n)
		if n > 0 {
			http.Redirect(w, r, "/redirect?n="+fmt.Sprint(n-1), http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("direct://"))
	}))
	defer server.Close()
	fetcher := Instance{}
	if _, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: server.URL + "/redirect?n=3", SourceType: "url"}); err != nil {
		t.Fatalf("allowed redirects failed: %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: server.URL + "/redirect?n=4", SourceType: "url"}); err == nil {
		t.Fatal("redirect limit was not enforced")
	}
	if _, err := fetcher.Fetch(context.Background(), resources.Subscription{Source: server.URL + "/large", SourceType: "url"}); err == nil {
		t.Fatal("body limit was not enforced")
	}
}

func TestFetchHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (Instance{}).Fetch(ctx, resources.Subscription{Source: server.URL, SourceType: "url"}); err == nil {
		t.Fatal("canceled fetch unexpectedly succeeded")
	}
}

func TestFetchRejectsInvalidHTTPSource(t *testing.T) {
	for _, source := range []string{"", "ftp://example.test/feed", "not a URL"} {
		if _, err := (Instance{}).Fetch(context.Background(), resources.Subscription{Source: source, SourceType: "url"}); err == nil {
			t.Fatalf("invalid source %q unexpectedly succeeded", source)
		}
	}
}
