package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// releaseGVR identifies the Release custom resource on a Konflux cluster.
var releaseGVR = schema.GroupVersionResource{
	Group:    "appstudio.redhat.com",
	Version:  "v1alpha1",
	Resource: "releases",
}

// collectInFlightReleases lists Release CRs from the live API and records the
// age of every one that is still running.
//
// This deliberately does not read KubeArchive. The archive config only accepts a
// Release once it has a completion time, so a Release that hangs is absent from
// the archive for as long as it matters. The live API is the only place a stuck
// release can be observed while it is still stuck.
//
// A Release counts as running when its Released condition is False with reason
// Progressing, which is the same rule releaseStatus() applies to archived
// Releases. Age is measured from the start time, falling back to creation.
func (e *KAExporter) collectInFlightReleases(ctx context.Context, namespaces []string) error {
	if e.dynClient == nil {
		return nil
	}

	ages := make(map[string][]float64, len(namespaces))
	now := time.Now().UTC()
	listed := 0

	for _, ns := range namespaces {
		list, err := e.dynClient.Resource(releaseGVR).Namespace(ns).List(ctx, metav1.ListOptions{Limit: kaPageLimit})
		if err != nil {
			if errors.IsForbidden(err) {
				// Surface this loudly rather than exporting an empty metric. A
				// silently absent series is indistinguishable from "nothing is
				// stuck", which is the failure mode this metric exists to avoid.
				return fmt.Errorf("listing releases is forbidden; the exporter service account needs get/list on releases.appstudio.redhat.com: %w", err)
			}
			if errors.IsNotFound(err) {
				// CRD absent, e.g. a cluster that runs no release service.
				return nil
			}
			return fmt.Errorf("list releases in %q: %w", ns, err)
		}

		listed += len(list.Items)
		for i := range list.Items {
			item := &list.Items[i]
			if !isUnstructuredReleaseRunning(item.Object) {
				continue
			}
			started := unstructuredString(item.Object, "status", "startTime")
			if started == "" {
				started = item.GetCreationTimestamp().UTC().Format(time.RFC3339)
			}
			startTime, err := time.Parse(time.RFC3339, started)
			if err != nil {
				continue
			}
			age := now.Sub(startTime).Seconds()
			if age < 0 {
				continue
			}
			ages[ns] = append(ages[ns], age)
		}
	}

	e.releaseInFlight.recordLive(e.cluster, ages)

	running := 0
	for _, list := range ages {
		running += len(list)
	}
	log.Printf("In-flight releases: %d running of %d listed across %d namespace(s)", running, listed, len(namespaces))
	return nil
}

// isUnstructuredReleaseRunning reports whether a Release has the Released
// condition set to False with reason Progressing.
func isUnstructuredReleaseRunning(obj map[string]interface{}) bool {
	status, ok := obj["status"].(map[string]interface{})
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] != "Released" {
			continue
		}
		return cond["status"] == "False" && cond["reason"] == "Progressing"
	}
	return false
}

// unstructuredString reads a nested string field, returning "" when any level
// is missing or of another type.
func unstructuredString(obj map[string]interface{}, path ...string) string {
	current := obj
	for i, key := range path {
		if i == len(path)-1 {
			value, _ := current[key].(string)
			return value
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

// newDynamicClient builds the client used for live Release lookups.
func newDynamicClient() (dynamic.Interface, error) {
	cfg, err := kubeRESTConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}
