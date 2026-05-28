package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	exporter, err := NewKAExporter()
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	// Propagate SIGINT/SIGTERM into context so goroutines and the HTTP server
	// can shut down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start background collection in a goroutine. Start() blocks until the first
	// collection completes, then closes readyCh, then enters the ticker loop.
	go exporter.Start(ctx)

	// Do not open /metrics until the initial collection has populated the cache.
	// This prevents Prometheus from recording a misleading empty scrape on startup.
	select {
	case <-exporter.readyCh:
		log.Printf("Initial collection complete — starting metrics server")
	case <-ctx.Done():
		log.Printf("Shutdown signalled before initial collection completed; exiting")
		return
	}

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(exporter)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
			Registry:          reg,
			// MaxRequestsInFlight removed: Collect() now performs no I/O, so
			// concurrent scrapes are safe and the 503 band-aid is unnecessary.
		},
	))
	// /healthz — liveness: the process is alive and the HTTP server is responsive.
	// Kubernetes restarts the pod only when this fails, so it is intentionally
	// unconditional — a temporarily-degraded exporter should not be restarted.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})
	// /health is kept for backward compatibility.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	// /readyz — readiness: at least one collection has succeeded and the last
	// success is not older than 2× the collection interval (+ 30s buffer).
	// Kubernetes removes the pod from the Service endpoints when this returns
	// non-200, so Prometheus will pause scraping until the exporter recovers.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ts := exporter.lastScrapeSuccessAt.Load()
		if ts == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "no successful scrape yet")
			return
		}
		staleThreshold := 2*exporter.collectInterval + 30*time.Second
		age := time.Since(time.Unix(ts, 0))
		if age > staleThreshold {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "last successful scrape was %.0fs ago (threshold: %.0fs)",
				age.Seconds(), staleThreshold.Seconds())
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	port := os.Getenv(portEnvVar)
	if port == "" {
		port = defaultPort
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Shut down the HTTP server when the context is cancelled.
	go func() {
		<-ctx.Done()
		log.Printf("Shutdown signal received; stopping HTTP server")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Printf("KubeArchive Prometheus Exporter started on http://0.0.0.0:%s/metrics", port)
	if exporter.fixedTenantNamespace != "" {
		namespaceCount := len(strings.Split(exporter.fixedTenantNamespace, ","))
		if namespaceCount == 1 {
			log.Printf("Cluster: %s, mode: single-tenant, TENANT_NAMESPACE=%s", exporter.cluster, exporter.fixedTenantNamespace)
		} else {
			log.Printf("Cluster: %s, mode: fixed-multi-tenant, TENANT_NAMESPACE=%s (%d namespaces)", exporter.cluster, exporter.fixedTenantNamespace, namespaceCount)
		}
	} else {
		log.Printf("Cluster: %s, mode: multi-tenant (namespaces with label %s=%s)", exporter.cluster, tenantLabelKey, tenantLabelValue)
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}
