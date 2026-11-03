package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Attempts int = iota
	Retry
)

type Backend struct {
	URL          *url.URL
	Weight        int
	CurrentWeight int
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

func parseBackendSpec(spec string) (*url.URL, int, error){
	weight:= 1
	raw :=spec

	if i := strings.LastIndex(spec, "|"); i >= 0 {
		raw = strings.TrimSpace(spec[:i])
		w, err := strconv.Atoi(strings.TrimSpace(spec[i+1:]))

		if err != nil {
			return nil, 0, fmt.Errorf("invalid weight in %q: %w", spec, err)
		}
		if w <= 0 {
			return nil, 0, fmt.Errorf("weight must be positive in %q", spec)
		}
		weight = w
	}
	serverUrl, err := url.Parse(raw)
	if err != nil {
		return nil, 0, err
	}
	return serverUrl, weight, nil
}

func (b *Backend) SetAlive(alive_status bool) {
	b.mux.Lock()
	b.Alive = alive_status
	b.mux.Unlock()
}

func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}

type ServerPool struct {
	backends []*Backend
	mu       sync.Mutex
}

func (s *ServerPool) AddBackend(backend *Backend) {
	if backend.Weight <= 0{
		backend.Weight=1
	}
	s.backends = append(s.backends, backend)
}

func (s *ServerPool) MarkBackendStatus(backendUrl *url.URL, alive bool) {
	for _, b := range s.backends {
		if b.URL.String() == backendUrl.String() {
			b.SetAlive(alive)
			break
		}
	}
}

func (s *ServerPool) GetNextPeer() *Backend {
	// Old round-robin selection (kept for reference):
	// next := s.NextIndex()
	// l := len(s.backends) + next
	// for i := next; i < l; i++ {
	// 	idx := i % len(s.backends)
	// 	if s.backends[idx].IsAlive() {
	// 		if i != next {
	// 			atomic.StoreUint64(&s.current, uint64(idx))
	// 		}
	// 		return s.backends[idx]
	// 	}
	// }
	// return nil

	s.mu.Lock()
	defer s.mu.Unlock()

	var selected *Backend
	total := 0

	for _, b := range s.backends {
		if !b.IsAlive() {
			continue
		}
		b.CurrentWeight += b.Weight
		total += b.Weight

		if selected == nil || b.CurrentWeight > selected.CurrentWeight {
			selected = b
		}
	}

	if selected == nil {
		return nil
	}
	selected.CurrentWeight -= total
	return selected
}

func (s *ServerPool) HealthCheck() {
	for _, b := range s.backends {
		status := "up"
		alive := isBackendAlive(b.URL)
		b.SetAlive(alive)
		if !alive {
			status = "down"
		}
		log.Printf("%s [%s]\n", b.URL, status)
	}
}

func GetAttemptsFromContext(r *http.Request) int {
	if attempts, ok := r.Context().Value(Attempts).(int); ok {
		return attempts
	}
	return 1
}

func GetRetryFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(Retry).(int); ok {
		return retry
	}
	return 0
}

func lb(w http.ResponseWriter, r *http.Request) {
	attempts := GetAttemptsFromContext(r)
	if attempts > 3 {
		log.Printf("%s(%s) Max attempts reached, terminating\n", r.RemoteAddr, r.URL.Path)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	peer := serverPool.GetNextPeer()
	if peer != nil {
		peer.ReverseProxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Service not available", http.StatusServiceUnavailable)
}

func isBackendAlive(u *url.URL) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		log.Println("Site unreachable, error: ", err)
		return false
	}
	defer conn.Close()
	return true
}

func healthCheck() {
	t := time.NewTicker(time.Minute * 2)
	for {
		select {
		case <-t.C:
			log.Println("Starting health check...")
			serverPool.HealthCheck()
			log.Println("Health check completed")
		}
	}
}

var serverPool ServerPool

func main() {
	var serverList string
	var port int
	flag.StringVar(&serverList, "backends", "", "Load balanced backends, use commas to separate")
	flag.IntVar(&port, "port", 3030, "Port to serve")
	flag.Parse()

	if len(serverList) == 0 {
		log.Fatal("Please provide one or more backends to load balance")
	}

	tokens := strings.Split(serverList, ",")
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		serverUrl, weight, err := parseBackendSpec(tok)
		if err != nil {
			log.Fatal(err)
		}

		proxy := httputil.NewSingleHostReverseProxy(serverUrl)
		proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, e error) {
			log.Printf("[%s] %s\n", serverUrl.Host, e.Error())
			retries := GetRetryFromContext(request)
			if retries < 3 {
				select {
				case <-time.After(10 * time.Millisecond):
					ctx := context.WithValue(request.Context(), Retry, retries+1)
					proxy.ServeHTTP(writer, request.WithContext(ctx))
				}
				return
			}

			serverPool.MarkBackendStatus(serverUrl, false)

			attempts := GetAttemptsFromContext(request)
			log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)
			ctx := context.WithValue(request.Context(), Attempts, attempts+1)
			lb(writer, request.WithContext(ctx))
		}

		serverPool.AddBackend(&Backend{
			URL:  serverUrl,
			Weight : weight,
			Alive:        true,
			ReverseProxy: proxy,
		})
		log.Printf("Configured server: %s (weight=%d)\n", serverUrl, weight)
	}

	if len(serverPool.backends) == 0 {
		log.Fatal("Please provide one or more backends to load balance")
	}

	server := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(lb),
	}

	go healthCheck()

	log.Printf("Load Balancer started at :%d\n", port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
