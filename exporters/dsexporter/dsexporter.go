package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const check = "prometheus-appstudio-ds"
var allDataSources = GetDataSources

type CustomCollector struct {
	konfluxUp *prometheus.GaugeVec
}

// Creating a new instance of CustomCollector.
func NewCustomCollector() *CustomCollector {
	return &CustomCollector{
		konfluxUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "grafana_ds_up",
			Help: "Availability of the Konflux default grafana datasource",
		},
		[]string{"check"}),
	}
}

// Describe method sends descriptions of the metrics to Prometheus.
// When Prometheus scrapes the /metrics endpoint of the exporter,
// it first calls the Describe method to get a description of all the metrics.
func (e *CustomCollector) Describe(ch chan<- *prometheus.Desc) {
	e.konfluxUp.Describe(ch)
}

// Collect method sends the current values of the metrics to Prometheus.
// After Prometheus understands what metrics are available (using the `Describe` method),
// it then calls the `Collect` method to actually get the values of those metrics.
func (e *CustomCollector) Collect(ch chan<- prometheus.Metric) {
	var availability float64
	clientset := kubernetes.NewForConfigOrDie(config.GetConfigOrDie())

	if grafanaRes, err := GetGrafanaResource(clientset); err == nil {
		if datasources, errB := allDataSources(grafanaRes); errB == nil {
			availability = GetDataSourceAvailability(datasources, check)
		}
	}

	e.konfluxUp.WithLabelValues(check).Set(availability)
	e.konfluxUp.Collect(ch)
}

// get the grafana resource as a map
func GetGrafanaResource(clientset *kubernetes.Clientset) (map[string]interface{}, error) {
	data, err := clientset.RESTClient().
		Get().
		AbsPath("/apis/grafana.integreatly.org/v1beta1").
		Namespace("appstudio-grafana").
		Resource("grafanas").
		Name("grafana-oauth").
		DoRaw(context.TODO())
	var grafanaResource map[string]interface{}
	err = json.Unmarshal(data, &grafanaResource)
	if err != nil {
		return nil, errors.New("Error getting grafana resource")
	}

	return grafanaResource, nil
}

// get datasources from grafana resource
func GetDataSources(grafanaResource map[string]interface{}) ([]string, error) {
	// return empty string slice if datasources are not defined
	if v, exists := grafanaResource["status"].(map[string]any); exists {
		if v["datasources"] == nil {
			return []string{}, nil
		}
	} else {
		return nil, errors.New("Error retrieving status key") 
	}
	datasourcesIfc := grafanaResource["status"].(map[string]any)["datasources"].([]interface{})
	datasources := make([]string, len(datasourcesIfc))
	for i, v := range datasourcesIfc {
		datasources[i] = v.(string)
	}
	return datasources, nil
}

// check if datasource exists, return 1 if yes, 0 if not
func GetDataSourceAvailability(datasources []string, dsToCheck string) float64 {
	for _, datasource := range datasources {
		if strings.Contains(datasource, dsToCheck) {
			fmt.Println("Datasource", datasource, "exists")
			return 1
		}
	}
	fmt.Println("Datasource", dsToCheck, "does not exist")
	return 0
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
