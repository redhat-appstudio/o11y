package main

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type CustomCollector struct {
	requestCounter prometheus.Counter
}

// Creating a new instance of CustomCollector.
func NewCustomCollector() *CustomCollector {
	return &CustomCollector{
		requestCounter: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "request_count",
			Help: "Number of requests handled by the handler",
		}),
	}
}

// Describe method sends descriptions of the metrics to Prometheus.
// When Prometheus scrapes the /metrics endpoint of the exporter,
// it first calls the Describe method to get a description of all the metrics.
func (e *CustomCollector) Describe(ch chan<- *prometheus.Desc) {
	e.requestCounter.Describe(ch)
}

// Collect method sends the current values of the metrics to Prometheus.
// After Prometheus understands what metrics are available (using the `Describe` method),
// it then calls the `Collect` method to actually get the values of those metrics.
func (e *CustomCollector) Collect(ch chan<- prometheus.Metric) {
	e.requestCounter.Collect(ch)
}

// Using a separate pedantic registry ensures that only your custom metric is exposed on the "/metrics" endpoint,
// providing a cleaner and more focused view. Also reduces potential noise from unrelated metrics.
func main() {
	reg := prometheus.NewPedanticRegistry()
	exporter := NewCustomCollector()
	reg.MustRegister(exporter)

	http.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
			Registry:          reg,
		},
	))
	fmt.Println("Server is listening on http://localhost:8090/metrics")
	http.ListenAndServe(":8090", nil)
}
