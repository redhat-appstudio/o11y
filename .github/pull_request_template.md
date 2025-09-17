### Description

<!-- Please provide a description of the changes. What is being changed (and why)? -->

---

### Pre-Submission Checklist

<!-- Before you submit the PR for review, please go through this checklist. -->

- [ ] **Jira Ticket:** The corresponding Jira ticket is linked in the description above, in the name of the PR and/or commit.
- [ ] **Alert Tests:** New or modified alerts include tests to ensure they function correctly.
- [ ] **SOP / Runbook:** Any required SOP has been created or updated as needed. If it has direct connection to the dashboard or alert - It should be included within the dashboard or alert's runbook label respectively. 
- [ ] **Dashboards Addition:** New dashboards or significant changes to them should include the link or a screen shot for validation purposes of the added content.
- [ ] **Contribution Guide:** This submission follows the guidelines in our [`README.md`](https://github.com/redhat-appstudio/o11y/blob/main/README.md) which contains additional information and examples.
- [ ] **Pipeline Finished Successfully**

---

### Deployment Notice

- [ ] **Production Deployment:** For any changes to [alerts](https://gitlab.cee.redhat.com/service/app-interface/-/blame/26fc0f896636ab30fda8718216804295422514a5/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L40), [recording rules](https://gitlab.cee.redhat.com/service/app-interface/-/blame/26fc0f896636ab30fda8718216804295422514a5/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L54) or [dashboards](https://gitlab.cee.redhat.com/service/app-interface/-/blame/26fc0f896636ab30fda8718216804295422514a5/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml#L38), I understand that the deployment of changes to production requires updating the commit reference to o11y in app-interface repository.

---

### Review

Once your PR is ready please let us know it the [#forum-konflux-o11y](https://redhat.enterprise.slack.com/archives/C04FDFTF8EB) slack channel. Tag `@konflux-o11y-ic` for assistance.
