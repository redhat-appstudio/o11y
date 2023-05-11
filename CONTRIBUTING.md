# Contributing

This document provides guidelines for contributing to this repository.

## Development Environment Setup
> **_IMPORTANT:_** Before contributing code to this repository, make sure to install the
pre-commit hooks as described
[here](https://source.redhat.com/departments/it/it-information-security/blog/python_version_of_rh_pre_commit_and_rh_gitleaks).

This section describes the main steps required before you can add your code and create a
 Pull Request to this repository.

### Prerequisites
* Linux machine with x86-64 architecture with bash installed.
* Basic tools: curl, tar, make.
* Python3 and pipenv. (run: `python3 -m pip install pipenv --user`)
* Go installed. Intructions in Go's [docs](https://go.dev/doc/install)

### Running the Automated Tests
1. Clone your fork of the project.
2. Execute `make all` to run the automated tests.

## Automated Tests
The following tools are currently used for automated testing within the project:
* [promtool](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/) - testing PromQL rules, queries and PromQL linting
* [pint](https://cloudflare.github.io/pint) - PromQL linting
* [gitlint](https://jorisroovers.com/gitlint/) - commit message linting
* [yamllint](https://yamllint.readthedocs.io) - YAML linting
* [dashboard-linter](https://github.com/grafana/dashboard-linter/blob/main/docs/index.md) - Grafana Dashboards linting

### Unit Tests
PromQL unit tests are stored in YAML files under [test/promql/tests](test/promql/tests).

Prometheus rules, stored as Kubernetes resources, are defined in
[prometheus/base/prometheus.rules.yaml](prometheus/base/prometheus.rules.yaml).
The Prometheus-related tools used for unit tests (and linting) cannot digest this
directly, so instead, the `prepare` target on the [Makefile](Makefile) extracts the
Prometheus configurations out of those configurations and stores them to a temporary
location: `test/promql/extracted-rules.yaml`.

The different test procedures should then refer to that temporary location as the 
location of the Prometheus rules. For example, a `promtool` test file
`test/promql/tests/my-test.yaml` should point to the rule file as follows:

```
rule_files:
  - ../extracted-rules.yaml
```

## Pull Requests
This section covers a few aspects to have in mind while working on or reviewing Pull
Requests.

### Test Coverage
Each Pull Request (PR) should include full test coverage for the code it introduces.

### Pull Request Span
Pull requests should be as small in size as possible.

The commits within a PR should convey the progression of the code between the point
before the PR was merged and after it was merged. They should NOT convey the progression
of the code within the PR lifecycle (e.g. no `addressing comments` commit messages).

### Commit Messages
The project is using [gitlint](https://jorisroovers.com/gitlint/) for enforcing commit
message structure. The linting rules are defined [here](.gitlint) and are enforced by
the CI system.

[Conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) is used as
the commit message standard.

If the commit message (also applicable to PR names and branch names) includes a Jira
ticket identifier (e.g. STONEO11Y-123), then it will automatically be referenced within
the Jira ticket.

Commit messages should be descriptive:
* The header should describe the purpose of the commit.
* The body should be written as if the reviewer does not know the story behind the
  commit.
* The body should provide the motivation behind the commit and an overview of how it
  works.

Example:
```
chore(STONEO11Y-21): add network egress metric and tests

- Create a new rule for getting the network transmit in bytes
and add the label_pipelines_appstudio_openshift_io_type to it.

- Add a Unit test file to test the metric and its products.

Signed-off-by: Avi Biton <abiton@redhat.com>
```

### Pull Request Description
The PR description (the first comment at the top of the PR) can usually be derived from
the commit message body - especially if the PR consists of a single commit.

On top of that, the PR description should include a bit of context, so that reviewers
won't have to perform extensive research just to be able to provide relevant code
review.

### Code Review Guidelines
* Each PR should be approved by at least 2 team members. Those approvals are only
relevant if given since the last major change in the PR content.

* All comments raised during code review should be addressed (fixed/replied).
  * Reviewers should resolve the comments that they've raised once they think they were
    properly addressed.
  * If a comment was addressed by the PR author but the reviewer did not resolve or
    reply within 1 workday (reviewer's workday), then the comment can be resolved by
    the PR author or by another reviewer.

* All new and existing automated tests should pass.

* A PR should be open for at least 1 workday at all time zones within the team. i.e.
team members from all time zones should have an opportunity to review the PR within
their workweek and their working hours.

* When reviewing a PR, verify that the PR addresses these points:
  * Edge cases
  * Race conditions
  * All new functionality is covered by unit tests
  * It should not be necessary to manually run the code to see if a certain part works,
    a test should cover it
  * The commits should be atomic, meaning that if we revert it, we don't lose something
    important that we didn't intend to lose
  * PRs should have a specific focus. If it can be divided into smaller standalone
    PRs, then it needs to be split up. The smaller the better
  * Check that the added functionality is not already possible with an existing
    part of the code
  * The code is maintainable and testable
  * The code and tests do not introduce instability to the testing framework
