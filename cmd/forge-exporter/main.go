package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := env("FORGE_EXPORTER_ADDR", ":9108")
	socket := env("FORGE_AGENT_METRICS_SOCKET", "/tmp/forge-agent-metrics.sock")
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		body, err := scrapeUnixMetrics(r.Context(), socket)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write(body)
	})
	log.Printf("forge exporter listening on %s and scraping %s", addr, socket)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func scrapeUnixMetrics(ctx context.Context, socket string) ([]byte, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("GET /metrics HTTP/1.1\r\nHost: forge-agent\r\nConnection: close\r\n\r\n")); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	for i := 0; i+3 < len(raw); i++ {
		if raw[i] == '\r' && raw[i+1] == '\n' && raw[i+2] == '\r' && raw[i+3] == '\n' {
			return raw[i+4:], nil
		}
	}
	return raw, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
