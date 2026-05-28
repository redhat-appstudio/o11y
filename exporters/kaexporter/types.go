package main

import "sync"

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

type SnapshotListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []Snapshot `json:"items"`
}

// ── Domain types ──────────────────────────────────────────────────────────────

// Snapshot is a subset of appstudio.redhat.com Snapshot CR JSON from KubeArchive.
type Snapshot struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace,omitempty"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Application string `json:"application"`
		Components  []struct {
			Name string `json:"name"`
		} `json:"components"`
	} `json:"spec"`
}

type PipelineRun struct {
	Metadata struct {
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

// snapshotIndex is an in-memory lookup for Snapshot CRs built page-by-page during streaming.
// Maps hold integer indices into store so they remain valid after slice reallocation.
type snapshotIndex struct {
	store      []Snapshot
	byBuildPLR map[string]int
	byName     map[string]int
}

func newSnapshotIndex() *snapshotIndex {
	return &snapshotIndex{
		byBuildPLR: make(map[string]int),
		byName:     make(map[string]int),
	}
}

// add copies each Snapshot from a page into the index.
func (idx *snapshotIndex) add(page []Snapshot) {
	for _, s := range page {
		i := len(idx.store)
		idx.store = append(idx.store, s) // copy — page slot is no longer referenced
		idx.byName[idx.store[i].Metadata.Name] = i
		if bplr := snapshotLabel(&idx.store[i], labelBuildPipelineRun); bplr != "" {
			idx.byBuildPLR[bplr] = i
		}
	}
}

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

// ── Outcome counting ──────────────────────────────────────────────────────────

// archivedOutcomeKey groups counts for konflux_archived_completion_count (Gauge).
type archivedOutcomeKey struct {
	namespace            string // tenant NS for build/test/release_cr; managed NS for release_plr
	applicationNamespace string // tenant NS for release_plr
	phase                string // build | test | release_cr | release_plr
	application          string
	component            string
	result               string
}

// safeOutcomeCounts wraps the outcome counts map with a mutex for thread-safe concurrent updates.
type safeOutcomeCounts struct {
	sync.Mutex
	counts map[archivedOutcomeKey]float64
}

// newSafeOutcomeCounts returns an empty safeOutcomeCounts.
func newSafeOutcomeCounts() *safeOutcomeCounts {
	return &safeOutcomeCounts{
		counts: make(map[archivedOutcomeKey]float64),
	}
}

func (s *safeOutcomeCounts) increment(key archivedOutcomeKey) {
	s.Lock()
	s.counts[key]++
	s.Unlock()
}

func (s *safeOutcomeCounts) getAll() map[archivedOutcomeKey]float64 {
	s.Lock()
	defer s.Unlock()
	result := make(map[archivedOutcomeKey]float64, len(s.counts))
	for k, v := range s.counts {
		result[k] = v
	}
	return result
}

// ── Snapshot correlation ──────────────────────────────────────────────────────

// resolveSnapshotNameForBuild returns the Snapshot name for a build PLR.
// It prefers the index (O(1)) over the PLR annotation, because KubeArchive may archive
// a PLR before the annotation is patched; the Snapshot label is the durable source.
func resolveSnapshotNameForBuild(tenantNS string, plr PipelineRun, idx *snapshotIndex) string {
	if idx == nil {
		return getAnnotation(plr, labelOrAnnotationSnapshot)
	}

	plrName := plr.Metadata.Name
	plrApp := getLabel(plr, labelAppStudioApp, "")
	plrComp := getLabel(plr, labelAppStudioComp, "")

	// Case 1: O(1) — build-pipelinerun label on the Snapshot.
	if i, ok := idx.byBuildPLR[plrName]; ok {
		s := &idx.store[i]
		if snapshotCRNamespace(s, tenantNS) == tenantNS && snapshotCompatibleWithPLR(s, plrApp, plrComp) {
			return s.Metadata.Name
		}
	}

	// Case 2: O(1) — annotation on the build PLR, validated against the index.
	if ann := getAnnotation(plr, labelOrAnnotationSnapshot); ann != "" {
		if i, ok := idx.byName[ann]; ok {
			s := &idx.store[i]
			if snapshotCRNamespace(s, tenantNS) == tenantNS {
				bp := snapshotLabel(s, labelBuildPipelineRun)
				if (bp == "" || bp == plrName) && snapshotCompatibleWithPLR(s, plrApp, plrComp) {
					return ann
				}
			}
		}
		// Annotation exists but snapshot not in index or failed validation — still trust the annotation.
		return ann
	}

	// Case 3: O(n) — component-based fallback, rare (group/heterogeneous snapshots).
	if plrComp != "" {
		var names []string
		for i := range idx.store {
			s := &idx.store[i]
			if snapshotCRNamespace(s, tenantNS) != tenantNS {
				continue
			}
			if s.Spec.Application != "" && s.Spec.Application != plrApp {
				continue
			}
			for _, c := range s.Spec.Components {
				if c.Name == plrComp {
					names = append(names, s.Metadata.Name)
					break
				}
			}
		}
		if len(names) == 1 {
			return names[0]
		}
	}

	return ""
}

// snapshotCRNamespace returns the namespace stored in the Snapshot metadata, or listNS if empty.
func snapshotCRNamespace(s *Snapshot, listNS string) string {
	if s.Metadata.Namespace != "" {
		return s.Metadata.Namespace
	}
	return listNS
}

// snapshotLabel returns the value of key from s.Metadata.Labels, or "" if absent.
func snapshotLabel(s *Snapshot, key string) string {
	if s.Metadata.Labels == nil {
		return ""
	}
	return s.Metadata.Labels[key]
}

// snapshotCompatibleWithPLR reports whether s's application and component labels are
// consistent with the given PLR's application and component (empty labels match anything).
func snapshotCompatibleWithPLR(s *Snapshot, plrApp, plrComp string) bool {
	sa := snapshotLabel(s, labelAppStudioApp)
	sc := snapshotLabel(s, labelAppStudioComp)
	if sa != "" && sa != plrApp {
		return false
	}
	if sc != "" && sc != plrComp {
		return false
	}
	if s.Spec.Application != "" && s.Spec.Application != plrApp {
		return false
	}
	return true
}

// ── Release correlation ───────────────────────────────────────────────────────

// findMatchingRelease returns the Release CR for a build PLR using the pre-built releaseIndex.
// It tries the build-pipelinerun label first (primary), then the snapshot label (fallback).
func findMatchingRelease(plr PipelineRun, snapshot, application, component string, idx *releaseIndex) *releaseEntry {
	if idx == nil {
		return nil
	}
	plrName := plr.Metadata.Name

	// O(1) primary: build-pipelinerun label.
	if i, ok := idx.byBuildPLR[plrName]; ok {
		re := &idx.store[i]
		rel := &re.Release
		if releaseLabelsCompatible(getLabel(*rel, labelAppStudioApp, ""), getLabel(*rel, labelAppStudioComp, ""), application, component) {
			return re
		}
	}

	// O(1) fallback: snapshot label.
	if snapshot != "" {
		if i, ok := idx.bySnapshot[snapshot]; ok {
			re := &idx.store[i]
			rel := &re.Release
			if releaseLabelsCompatible(getLabel(*rel, labelAppStudioApp, ""), getLabel(*rel, labelAppStudioComp, ""), application, component) {
				return re
			}
		}
	}

	return nil
}

// releaseLabelsCompatible reports whether a Release's app/component labels are consistent
// with the given PLR's app/component (empty Release labels match anything).
func releaseLabelsCompatible(relApp, relComp, plrApp, plrComp string) bool {
	if relApp != "" && relApp != plrApp {
		return false
	}
	if relComp != "" && relComp != plrComp {
		return false
	}
	return true
}
