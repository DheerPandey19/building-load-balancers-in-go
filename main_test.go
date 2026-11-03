package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

func addTestBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(name))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverPool.AddBackend(&Backend{
		URL:          u,
		Alive:        true,
		ReverseProxy: httputil.NewSingleHostReverseProxy(u),
	})
	return srv
}

func TestRoundRobinAndProxy(t *testing.T) {
	serverPool = ServerPool{}
	addTestBackend(t, "A")
	addTestBackend(t, "B")

	got := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		lb(rec, req)
		res := rec.Result()
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", res.StatusCode, body)
		}
		got = append(got, string(body))
	}

	if got[0] == got[1] {
		t.Fatalf("expected alternating backends, got %v", got)
	}
	if got[0] != got[2] || got[1] != got[3] {
		t.Fatalf("expected A,B,A,B pattern, got %v", got)
	}
}

func TestSkipsDeadBackend(t *testing.T) {
	serverPool = ServerPool{}
	addTestBackend(t, "A")
	addTestBackend(t, "B")
	serverPool.backends[0].SetAlive(false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	lb(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if string(body) != "B" {
		t.Fatalf("expected live backend B, got %q", body)
	}
}

func TestServiceUnavailableWhenAllDown(t *testing.T) {
	serverPool = ServerPool{}
	addTestBackend(t, "A")
	serverPool.backends[0].SetAlive(false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	lb(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}
