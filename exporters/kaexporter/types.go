package main

// ── KubeArchive API response envelopes ────────────────────────────────────────

type ListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []PipelineRun `json:"items"`
}

type ReleaseListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []Release `json:"items"`
}

// ── Domain types ──────────────────────────────────────────────────────────────

type PipelineRun struct {
	Metadata struct {
		UID               string            `json:"uid"`
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		StartTime      string      `json:"startTime"`
		CompletionTime string      `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

type Release struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace,omitempty"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
        ReleasePlan string `json:"releasePlan"`
        Snapshot    string `json:"snapshot"`
    } `json:"spec"`
	Status struct {
		StartTime      string      `json:"startTime"`
		CompletionTime string      `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status,omitempty"`
	Reason             string `json:"reason"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	Message            string `json:"message,omitempty"`
}
type releaseIntentKey struct {
    Namespace   string
    Snapshot    string
    ReleasePlan string
}

// ── In-memory indexes ─────────────────────────────────────────────────────────

// releaseEntry is a Release CR plus the namespace it was listed
type releaseEntry struct {
	Release
	crNamespace string
}

// releaseIndex stores Release CRs for metric collection and retry analysis.
type releaseIndex struct {
	store []releaseEntry
}

// newReleaseIndex returns an empty releaseIndex ready to receive releases.
func newReleaseIndex() *releaseIndex {
	return &releaseIndex{}
}

func resolveSnapshot(r Release) string {
	if snap := getLabel(r, labelReleaseSnapshot, ""); snap != "" {
		return snap
	}
	return r.Spec.Snapshot
}

func resolveReleasePlan(r Release) string {
	return r.Spec.ReleasePlan
}

// addReleases copies releases from a namespace into the index.
func (idx *releaseIndex) addReleases(ns string, releases []Release) {
	for _, r := range releases {
		crNS := r.Metadata.Namespace
		if crNS == "" {
			crNS = ns
		}
		idx.store = append(idx.store, releaseEntry{Release: r, crNamespace: crNS})
	}
}
