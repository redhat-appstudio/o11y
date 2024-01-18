package main

import (
	"testing"
	"reflect"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/assert"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestGetDataSourceAvailability(t *testing.T) {
	datasources := []string{"appstudio-grafana/prometheus-appstudio-ds/b83cfcd5-3012-4e6a-b044-6923e1fef2d8", "prometheus-appstudio/b83cfcd5-3012-4e6a-b044-6923e1fef2d8", "appstudio-ds"}
	check := "prometheus-appstudio-ds"
	result := GetDataSourceAvailability(datasources, check)
	if result != 1 {
		t.Errorf("Expected result 1, but got %f", result)
	}

	check = "prometheus-appstudio-dsexporter"
	result = GetDataSourceAvailability(datasources, check)
	if result != 0 {
		t.Errorf("Expected result 0, but got %f", result)
	}
}

func TestGetGrafanaResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/grafana.integreatly.org/v1beta1/namespaces/appstudio-grafana/grafanas/grafana-oauth" {
			t.Errorf("Unexpected request path: %s", r.URL.Path)
		}
		response := `{"test": "data"}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := &rest.Config{
		Host: server.URL,
	}
	clientset, err := kubernetes.NewForConfig(config)
	if(err != nil) {
		t.Fatalf("Error: %v", err)
	}
	result, errB := GetGrafanaResource(clientset)
	expectedResult := map[string]interface{}{"test": "data"}

	if errB != nil {
		t.Errorf("Error %v when getting grafana resources", errB)
	}

	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Expected %v, but got %v", expectedResult, result)
	}
}

func TestGetDataSources(t *testing.T) {
	grafanaRes := map[string]interface{}{
		"status": map[string]interface{}{
			"datasources": []interface{}{"appstudio-ds1", "appstudio-ds2"},
		},
	}

	expectedResult := []string{"appstudio-ds1", "appstudio-ds2"}
	result, err := GetDataSources(grafanaRes)

	if err != nil {
		t.Errorf("Error %v when getting datasources", err)
	}

	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Test-1 failed, Expected %v, but got %v", expectedResult, result)
	}

	// empty datasources
	grafanaRes = map[string]interface{}{
		"status": map[string]interface{}{
			"datasources": []interface{}{},
		},
	}

	expectedResult = []string{}
	result, _ = GetDataSources(grafanaRes)
	
	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Test-2 failed, Expected %v, but got %v", expectedResult, result)
	}

	grafanaRes = map[string]interface{}{
		"data": map[string]interface{}{
			"datasources": []interface{}{"appstudio-ds1", "appstudio-ds2"},
		},
	}

	_, err = GetDataSources(grafanaRes)
	assert.ErrorContains(t, err, "Error retrieving status key")
}

func MockGetDataSources(grafanaResource map[string]interface{}) ([]string, error) {
	return []string{"appstudio-ds1", "appstudio-ds2"}, nil
}

func MockGetDataSourcesExists(grafanaResource map[string]interface{}) ([]string, error) {
	return []string{"appstudio-ds1", "appstudio-grafana/prometheus-appstudio-ds/b83cfcd5-3012-4e6a-b044-6923e1fef2d8"}, nil
}

func TestMain(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	exporter := NewCustomCollector()
	reg.MustRegister(exporter)

	allDataSources = MockGetDataSources
	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	res, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("Failed to perform GET request: %v", err)
	}
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, 1, testutil.CollectAndCount(exporter))
	assert.Equal(t, float64(0), testutil.ToFloat64(exporter))

	allDataSources = MockGetDataSourcesExists
	assert.Equal(t, float64(1), testutil.ToFloat64(exporter))
}
