---
name: vig
description: Use vig as a determinism layer over report data and open a local vig dashboard for findings
---

vig is a reporting dashboard and determinism layer for security audit reports. It means it provides a local dashboard visualizations that act as the final report format, and its APIs provide strict rules to adhere to when submitting and working with security findings.

Ensure the user runs vig locally and install if not, and submit all findings to satisfy vig's format requirements to the best quality you can.

When done, use the browser pane or user's browser opening preferences to show the vig report per the current engagement.

Everything an author needs to produce a report that passes is in the API:
- GET /v1/findings/shape lists the required, recommended and optional fields, the rules validation applies, and a filled example.
- Every field's description in the OpenAPI document opens with Required, Recommended or Optional.
- POST /v1/op/jobs/{id}/report/check is a dry run: what would be refused, and what the report reads better with.
- A claimed job carries the lessons from earlier reports on the same repository.

The vig repo is maintained at https://github.com/forefy/vig

Vig docs: https://github.com/forefy/vig/tree/main/docs
