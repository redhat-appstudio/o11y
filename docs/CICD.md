## CI/CD overview

Continuous Integration is handled by Konflux. It runs as Tekton pipelines via Pipelines-as-Code (PaC) in the `rhtap-o11y-tenant` namespace. Pipeline definitions live in `.tekton/`. This CI pipeline verifies all components of this repository by running their tests. Each repository component has specific testing and deployment process, which is documented in the component `.md` file.

For Alert and Recording rules, see the Deployment Model section in [ALERTS.md](ALERTS.md) and [RECORDING.md](RECORDING.md) respectively.
For Grafana dashboards, see the Deployment Model section in [DASHBOARDS.md](DASHBOARDS.md).
For exporters, see the Deployment Model section in [EXPORTERS.md](EXPORTERS.md).

## Pipelines

### Pull request pipeline (`.tekton/o11y-pull-request.yaml`)

Triggers on PRs targeting `main` and runs **repo-specific validation tekton tasks in parallel**. These tasks are defined inline in the pipeline YAML or as local task files. It should run all available tests and lints and block merging until all issues are addressed.

### Push pipeline (`.tekton/o11y-push.yaml`)

Triggers on pushes to `main`. Runs the build and security chain **only**. Produces a signed container image in the tenant registry (`quay.io/redhat-user-workloads/rhtap-o11y-tenant/...`). The image is promoted to the production registry (`quay.io/redhat-appstudio/o11y`) by the release pipeline after the Enterprise Contract check passes (see [Enterprise Contract and release](#enterprise-contract-and-release)).

If the push pipeline fails after merge, there is no proactive notification — the pipeline does not have a Slack webhook task configured. Failures are only visible as GitHub check runs on the merge commit or in the Konflux UI. Konflux supports adding a [`slack-webhook-notification`](https://konflux-ci.dev/docs/patterns/slack-notifications/) task to the pipeline's `finally` block, but this is not currently set up.

### Debugging failed pipelines

Pipeline results appear as GitHub check runs on the PR. Click the **Details** link on `Red Hat Konflux / o11y-on-pull-request` to open the pipeline run in the Konflux UI (requires cluster SSO login). The UI shows per-task logs and status.

To **re-trigger** a failed PR pipeline, comment `/retest` on the PR.

To **re-trigger** a failed push pipeline, annotate the Component with `build.appstudio.openshift.io/request=trigger-pac-build` or use the Konflux UI to rerun the pipeline run.

### Debugging failed releases

Release objects live in the o11y tenant namespace:

```
oc get releases -n rhtap-o11y-tenant
```

Each Release has a `status.managedProcessing.pipelineRun` field pointing to the associated pipeline run in `rhtap-releng-tenant` (e.g. `rhtap-releng-tenant/managed-5lc82`). The release status and failure reason are also visible in the Konflux UI under **Applications** > **o11y** > **Releases**.

To re-trigger a failed release without rebuilding the image, create a new Release object from the failed one. See [Re-trigger a release manually](https://konflux-ci.dev/docs/releasing/retrigger-release/) for the procedure. Alternatively, re-triggering the push pipeline rebuilds the image and creates a new release automatically, but is slower since it restarts the entire build-EC-release chain.

### Dependency updates

[MintMaker](https://github.com/konflux-ci/mintmaker), a Renovate-based service, automatically opens PRs (from `red-hat-konflux[bot]`) to bump pinned references across the repo: Tekton pipeline task bundles in `.tekton/`, Dockerfile base image digests/tags, and Python and Go dependencies. MintMaker does not touch the repo-specific validation tasks -- the o11y team owns their logic and container image versions.

## Local vs CI execution

`make all` runs the full local validation suite. The Makefile spawns the `obsctl-reloader-rules-checker` container for rule checks. In CI, the pipeline step already runs inside that container image, so it overrides the command via `CMD=obsctl-reloader-rules-checker make check-and-test` -- no container-in-container needed.

## Merge requirements

PRs targeting `main` require all of the following (enforced for admins too):

- **Pipeline passes** -- `Red Hat Konflux / o11y-on-pull-request` is the only required status check. Strict mode is off, so the branch doesn't need to be rebased on `main` before merging. It is recommended to keep feature branch updated to avoid possible conflicts.
- **2 approving reviews** -- stale review dismissal is off, so approvals survive new pushes.
- **All conversations resolved**

## Enterprise Contract and release

After every build (PR and push), Konflux runs an **Enterprise Contract (EC)** check that verifies the container image against supply chain policy. On push, if the EC passes, the image is automatically released. The EC check also appears on PRs as `Red Hat Konflux / o11y-enterprise-contract / o11y` but is not a required status check.

### Release data configuration

Release configuration lives in the [konflux-release-data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository. The o11y-related files are split across folders that reflect separation of concerns:

- **Tenant setup** (`tenants-config/.../admin/rhtap-o11y-tenant/`) -- namespace, resource quotas, and limits for the `rhtap-o11y-tenant` namespace where o11y's Konflux application runs.
- **Tenant workloads** (`tenants-config/.../tenants/rhtap-o11y-tenant/`) -- resources the o11y team owns:
  - `integration_test_scenario.yaml` -- triggers the EC pipeline after every build, using the shared `app-interface-standard` policy.
  - `release_plan.yaml` -- enables auto-release, targeting `rhtap-releng-tenant`.
- **Release engineering** (`config/stone-prd-rh01.pg1f.p1/service/ReleasePlanAdmission/rhtap-o11y/`) -- release engineering's acceptance of the release plan. `o11y.yaml` defines how the release happens: pushes the image to `quay.io/redhat-appstudio/o11y`, tags it with git SHA and `latest`.
- **Constraints** (`constraints/service/rhtap-o11y.yaml`) -- schema validation that ensures o11y can only use the `app-interface-standard` policy, push to approved registries, and use the approved release pipeline.
