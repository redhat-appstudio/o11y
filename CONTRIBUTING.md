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
* Basic tools: `curl`, `tar`, `make``.
* Some container engine: either `docker` or `podman`.
* go: v1.20
* Python 3.9 and pipenv.

### Running the Automated Tests
1. Clone your fork of the project.
2. Execute `make all` to run the automated tests.

## Automated Tests
The following tools are currently used for automated testing within the project:
* [`promtool`](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/) - testing PromQL rules, queries and PromQL linting
* [`pint`](https://cloudflare.github.io/pint) - PromQL linting
* [`gitlint`](https://jorisroovers.com/gitlint/) - commit message linting
* [`yamllint`](https://yamllint.readthedocs.io) - YAML linting

Those tools do not need to be installed on your computer.

All those tools (except `gitlint`) are bundled in the
[`obsctl-reloader-rules-checker`](https://github.com/rhobs/obsctl-reloader-rules-checker/tree/main)
container image used by the CI or when testing locally with `make`.

### Unit Tests
PromQL unit tests are stored in YAML files under [`test/promql/tests`](test/promql/tests).

Those tests reference the rule files to test via the `rule_files` attribute.
This attribute has to list the names of files in the [`rhobs/alerting`](rhobs/alerting) directory.

For example, if a test file, relies on rules in files `rhobs/alerting/some-rules.yaml`
and `rhobs/alerting/more-rules.yaml`, the test file needs to references the rules
in the following way:
```
rule_files:
  - some-rules.yaml
  - more-rules.yaml
```

This repository contains Go unit tests. Test files are located in the `o11y/exporters`
directory with `_test.go` file names.

To include all go packages in the current directory and its subdirectories run
`go get ./...` This command will download and install all the dependencies mentioned
in the import statements of the project `o11y/exporters/<go files>`.

To run all the Go unit tests in the repository, execute the following command from
the project's root directory `go test ./...` a similar output is expected:

`ok      github.com/redhat-appstudio/o11y.git/exporters/dummy_service_exporter   0.002s`

In order to run tests in a specific directory/path, you need to specify the package path,
as shown below:

`go test ./<directory/path>`


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
