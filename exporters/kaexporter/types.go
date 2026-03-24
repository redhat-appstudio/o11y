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

// ── In-memory indexes ─────────────────────────────────────────────────────────

// releaseEntry is a Release CR plus the namespace it was listed
type releaseEntry struct {
	Release
	crNamespace string
}

// releaseIndex is a dual-keyed lookup for build-PLR → Release correlation.
type releaseIndex struct {
	store      []releaseEntry
	byBuildPLR map[string]int
	bySnapshot map[string]int
}

// newReleaseIndex returns an empty releaseIndex ready to receive releases.
func newReleaseIndex() *releaseIndex {
	return &releaseIndex{
		byBuildPLR: make(map[string]int),
		bySnapshot: make(map[string]int),
	}
}

// addReleases copies releases from a namespace into the index.
func (idx *releaseIndex) addReleases(ns string, releases []Release) {
	for _, r := range releases {
		i := len(idx.store)
		crNS := r.Metadata.Namespace
		if crNS == "" {
			crNS = ns
		}
		idx.store = append(idx.store, releaseEntry{Release: r, crNamespace: crNS})

		// Primary key: build-pipelinerun label — overwrite so the latest release wins.
		if bplr := getLabel(r, labelBuildPipelineRun, ""); bplr != "" {
			idx.byBuildPLR[bplr] = i
		}
		// Fallback key: snapshot label — keep the first to avoid overwriting with retries.
		if snap := getLabel(r, "release.appstudio.openshift.io/snapshot", ""); snap != "" {
			if _, exists := idx.bySnapshot[snap]; !exists {
				idx.bySnapshot[snap] = i
			}
		}
	}
}
