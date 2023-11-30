package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestCustomCollector(t *testing.T) {
	exporter := NewCustomCollector()
	prometheus.MustRegister(exporter)

	// Simulate collecting metrics and check the exported metric value.
	metrics := prometheus.DefaultGatherer
	metricFamilies, err := metrics.Gather()
	assert.NoError(t, err)

	var RequestCountValue float64
	for _, mf := range metricFamilies {
		if mf.GetName() == "request_count" {
			RequestCountValue = mf.GetMetric()[0].GetCounter().GetValue()
			break
		}
	}

	// Check whether the exported metric value is initially 0.
	assert.Equal(t, float64(0), RequestCountValue)

	// Increment the requestCounter by calling the Inc method.
	exporter.requestCounter.Inc()

	// Collecting metrics again
	metricFamilies, err = metrics.Gather()
	assert.NoError(t, err)

	for _, mf := range metricFamilies {
		if mf.GetName() == "request_count" {
			RequestCountValue = mf.GetMetric()[0].GetCounter().GetValue()
			break
		}
	}

	// Check whether the exported metric value is now 1 after incrementing.
	assert.Equal(t, float64(1), RequestCountValue)
}
