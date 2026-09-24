package main

// ── LabelSet test constants ─────────────────────────────────────────────────

var (
	buildLabelShort = LabelSet{
		Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
		BuildType: "docker-builds", EventType: "push",
	}
	integrationLabelShort = LabelSet{
		Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
		Scenario: "my-scenario", Optional: "false", TestType: "integration", EventType: "push",
	}
	releaseLabelShort = LabelSet{
		Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
		Automated: "true", EventType: "push",
	}
)

// ── SLOConfig builder ───────────────────────────────────────────────────────

type sloConfigBuilder struct {
	cfg SLOConfig
}

func newSLOCfg() *sloConfigBuilder {
	return &sloConfigBuilder{}
}

//nolint:unused
func (b *sloConfigBuilder) buildThreshold(v float64) *sloConfigBuilder {
	b.cfg.BuildDurationThresholdSeconds = thresholdValueSimple(v)
	return b
}

//nolint:unused
func (b *sloConfigBuilder) buildBreachPct(v float64) *sloConfigBuilder {
	b.cfg.BuildDurationBreachPercentage = thresholdValueSimple(v)
	return b
}

//nolint:unused
func (b *sloConfigBuilder) integrationThreshold(v float64) *sloConfigBuilder {
	b.cfg.IntegrationDurationThresholdSeconds = thresholdValueSimple(v)
	return b
}

//nolint:unused
func (b *sloConfigBuilder) integrationBreachPct(v float64) *sloConfigBuilder {
	b.cfg.IntegrationDurationBreachPercentage = thresholdValueSimple(v)
	return b
}

//nolint:unused
func (b *sloConfigBuilder) releaseThreshold(v float64) *sloConfigBuilder {
	b.cfg.ReleaseDurationThresholdSeconds = thresholdValueSimple(v)
	return b
}

func (b *sloConfigBuilder) releaseBreachPct(v float64) *sloConfigBuilder {
	b.cfg.ReleaseDurationBreachPercentage = thresholdValueSimple(v)
	return b
}

func (b *sloConfigBuilder) tenant(name string) *tenantBuilder {
	if b.cfg.Tenants == nil {
		b.cfg.Tenants = make(map[string]*TenantSLOConfig)
	}
	t := &TenantSLOConfig{}
	b.cfg.Tenants[name] = t
	return &tenantBuilder{root: b, cfg: t}
}

func (b *sloConfigBuilder) build() *SLOConfig {
	return &b.cfg
}

// ── Tenant builder ──────────────────────────────────────────────────────────

type tenantBuilder struct {
	root *sloConfigBuilder
	cfg  *TenantSLOConfig
}

func (tb *tenantBuilder) buildThreshold(v float64) *tenantBuilder {
	tb.cfg.BuildDurationThresholdSeconds = thresholdValueSimple(v)
	return tb
}

//nolint:unused
func (tb *tenantBuilder) buildBreachPct(v float64) *tenantBuilder {
	tb.cfg.BuildDurationBreachPercentage = thresholdValueSimple(v)
	return tb
}

func (tb *tenantBuilder) integrationThreshold(v float64) *tenantBuilder {
	tb.cfg.IntegrationDurationThresholdSeconds = thresholdValueSimple(v)
	return tb
}

//nolint:unused
func (tb *tenantBuilder) integrationBreachPct(v float64) *tenantBuilder {
	tb.cfg.IntegrationDurationBreachPercentage = thresholdValueSimple(v)
	return tb
}

//nolint:unused
func (tb *tenantBuilder) releaseThreshold(v float64) *tenantBuilder {
	tb.cfg.ReleaseDurationThresholdSeconds = thresholdValueSimple(v)
	return tb
}

//nolint:unused
func (tb *tenantBuilder) releaseBreachPct(v float64) *tenantBuilder {
	tb.cfg.ReleaseDurationBreachPercentage = thresholdValueSimple(v)
	return tb
}

func (tb *tenantBuilder) app(name string) *appBuilder {
	if tb.cfg.Applications == nil {
		tb.cfg.Applications = make(map[string]*ApplicationSLOConfig)
	}
	a := &ApplicationSLOConfig{}
	tb.cfg.Applications[name] = a
	return &appBuilder{tenant: tb, cfg: a}
}

func (tb *tenantBuilder) build() *SLOConfig {
	return tb.root.build()
}

// ── App builder ─────────────────────────────────────────────────────────────

type appBuilder struct {
	tenant *tenantBuilder
	cfg    *ApplicationSLOConfig
}

func (ab *appBuilder) buildThreshold(v float64) *appBuilder {
	ab.cfg.BuildDurationThresholdSeconds = thresholdValueSimple(v)
	return ab
}

//nolint:unused
func (ab *appBuilder) buildBreachPct(v float64) *appBuilder {
	ab.cfg.BuildDurationBreachPercentage = thresholdValueSimple(v)
	return ab
}

//nolint:unused
func (ab *appBuilder) integrationThreshold(v float64) *appBuilder {
	ab.cfg.IntegrationDurationThresholdSeconds = thresholdValueSimple(v)
	return ab
}

//nolint:unused
func (ab *appBuilder) integrationBreachPct(v float64) *appBuilder {
	ab.cfg.IntegrationDurationBreachPercentage = thresholdValueSimple(v)
	return ab
}

func (ab *appBuilder) releaseThreshold(v float64) *appBuilder {
	ab.cfg.ReleaseDurationThresholdSeconds = thresholdValueSimple(v)
	return ab
}

//nolint:unused
func (ab *appBuilder) releaseBreachPct(v float64) *appBuilder {
	ab.cfg.ReleaseDurationBreachPercentage = thresholdValueSimple(v)
	return ab
}

func (ab *appBuilder) comp(name string, thresholds SLOThresholds) *appBuilder {
	if ab.cfg.Components == nil {
		ab.cfg.Components = make(map[string]*ComponentSLOConfig)
	}
	ab.cfg.Components[name] = &ComponentSLOConfig{SLOThresholds: thresholds}
	return ab
}

func (ab *appBuilder) build() *SLOConfig {
	return ab.tenant.build()
}
