# Dalec Assurance Case

## Purpose
- Provide the structured argument required for OpenSSF Best Practices **Silver**, demonstrating that Dalec's release process mitigates material supply-chain risks.
- Map high-level claims to stable evidence maintained in the repository and organizational policies.

## Scope and Context
- **System under consideration:** The Dalec BuildKit frontend, supporting targets, packaging assets, and tooling described in ARCHITECTURE.md#runtime-components and ARCHITECTURE.md#build-orchestration-flow.
- **Operational context:** Releases are produced from the `main` branch by GitHub Actions workflows executed by maintainers using the documented development workflow.
- **Audience:** Dalec maintainers, release engineers, CNCF reviewers, and downstream consumers evaluating the project's security posture.
- **Assumptions:** Upstream dependencies (Docker, BuildKit, Go toolchain, GitHub) maintain their security guarantees and the organization applies the documented policies.

## Threat Model
- **Assets:** Dalec’s BuildKit frontend runtime, target plugins, packaging templates, optional SBOM and provenance generation features, and the packages or container images created when users build with Dalec.
- **Actors:** Dalec maintainers and trusted contributors; potentially malicious or compromised spec authors; adversaries controlling remote source origins; threat actors attempting to exploit vulnerabilities in Dalec’s runtime or generated artifacts.
- **Attack vectors:** Injection of hostile build steps or packaging metadata through Dalec specs, compromise of external source artifacts fetched during builds, tampering with SBOM/provenance metadata when generated, misuse of plugin extension points to bypass sandboxing, and exploitation of defects in the frontend, packaging logic, or integration with BuildKit.
- **Mitigations:** Declarative spec validation, BuildKit’s sandboxed execution with clearly defined mounts, optional signed-package/SBOM features available to spec authors, tests defined in specs and executed via the Dalec test runner, and ongoing dependency/static analysis to surface exploitable defects. (Note: official release artifacts do not yet ship with signatures or SBOM/provenance outputs; see “Residual Risks and Planned Enhancements”.)

## Top-Level Claim (G0)
> Dalec governance, development, build, and release practices provide sufficient assurance that released artifacts maintain expected security characteristics.

The remainder of this document decomposes G0 into supporting strategies, claims, and evidence.

### Strategy S1 – Community Governance Provides Accountable Oversight
- **Claim C1.1:** Maintainer-led governance enforces openness, fairness, and participation.  
  *Evidence:* [GOVERNANCE.md#values](../GOVERNANCE.md#values), [CONTRIBUTOR_LADDER.md](../CONTRIBUTOR_LADDER.md).
- **Claim C1.2:** Maintainers collectively own security response responsibilities.  
  *Evidence:* [GOVERNANCE.md#security-response-team](../GOVERNANCE.md#security-response-team).

### Strategy S2 – Secure Development Lifecycle Controls Prevent Unauthorized Changes
- **Claim C2.1:** Authorship attestation is required before code is accepted via DCO sign-off.  
  *Evidence:* [README.md#contributing](../README.md#contributing) (DCO requirement), [CONTRIBUTING.md#contributing-a-patch](../CONTRIBUTING.md#contributing-a-patch) (CNCF `dco-2` status check).
- **Claim C2.2:** Branch protection rules enforce at least one independent maintainer approval and block merges without passing required checks.  
  *Evidence:* [GOVERNANCE.md#branch-protection](../GOVERNANCE.md#branch-protection), [GOVERNANCE.md#code-changes](../GOVERNANCE.md#code-changes).
- **Claim C2.3:** All organization members authenticate with secure multi-factor credentials (hardware FIDO2 keys or app-based TOTP/WebAuthn; SMS and email factors are not permitted).  
  *Evidence:* [SECURITY.md#maintainer-authentication](../SECURITY.md#maintainer-authentication).
- **Claim C2.4:** Continuous integration runs mandatory static analysis and linting before merge.  
  *Evidence:* [.github/workflows/ci.yml](../.github/workflows/ci.yml) (golangci-lint, custom linters, generated file validation), [.github/workflows/codeql.yml](../.github/workflows/codeql.yml) (GitHub CodeQL analysis).
- **Claim C2.5:** Architecture and extension points are documented to guide safe contributions.  
  *Evidence:* [ARCHITECTURE.md#runtime-components](../ARCHITECTURE.md#runtime-components), [ARCHITECTURE.md#specification](../ARCHITECTURE.md#specification), [ARCHITECTURE.md#build-orchestration-flow](../ARCHITECTURE.md#build-orchestration-flow).
- **Claim C2.6:** Secrets and credentials are managed outside source control: CI and release workflows rely solely on GitHub’s scoped `GITHUB_TOKEN`, and build-time secrets for signing flows are supplied through BuildKit secret mounts as documented for spec authors.  
  *Evidence:* [SECURITY.md#secret-management](../SECURITY.md#secret-management), [Signing secrets guide](https://project-dalec.github.io/dalec/signing#secrets).

### Strategy S3 – Build and Release Pipeline Preserves Artifact Integrity
- **Claim C3.1:** Build orchestration is declaratively defined and executed via BuildKit using configuration checked into the repository, and the automated release workflow applies the same configuration when publishing official images.  
  *Evidence:* [ARCHITECTURE.md#build-orchestration-flow](../ARCHITECTURE.md#build-orchestration-flow), [ARCHITECTURE.md#runtime-components](../ARCHITECTURE.md#runtime-components), [.github/workflows/release.yml](../.github/workflows/release.yml).
- **Claim C3.2:** Generated assets and schemas are validated before merge by running `go generate ./...` in CI and failing if diffs appear.  
  *Evidence:* [.github/workflows/ci.yml](../.github/workflows/ci.yml), [spec.go](../spec.go) (Go generate directive for schema regeneration).
- **Claim C3.3:** The release workflow builds and pushes frontend images to GHCR via GitHub Actions using BuildKit; while signatures and SBOM/provenance publication are not yet enabled, the consistent pipeline and protected branches mitigate tampering risk and the gap is tracked for remediation.  
  *Evidence:* [.github/workflows/release.yml](../.github/workflows/release.yml), [.github/workflows/frontend-image.yml](../.github/workflows/frontend-image.yml), [GOVERNANCE.md#branch-protection](../GOVERNANCE.md#branch-protection).
- **Claim C3.4:** Dependency risks are continuously monitored and triaged: Dependabot opens update pull requests that maintainers review and merge after verification, the GitHub Dependency Review workflow flags vulnerable changes during code review, and the Snyk GitHub App surfaces new advisories in the security dashboard for maintainer remediation.  
  *Evidence:* [.github/dependabot.yml](../.github/dependabot.yml), [.github/workflows/dependency-review.yml](../.github/workflows/dependency-review.yml), [SECURITY.md#dependency-monitoring](../SECURITY.md#dependency-monitoring) (Dependabot and Snyk coverage).

### Strategy S4 – Vulnerability Management and Incident Response Contain Risk
- **Claim C4.1:** Researchers have a private disclosure channel with clear guidance.  
  *Evidence:* [SECURITY.md#reporting-security-issues](../SECURITY.md#reporting-security-issues).
- **Claim C4.2:** Maintainers publish advisories after remediation to inform users about impact and mitigation steps.  
  *Evidence:* [SECURITY.md#communication](../SECURITY.md#communication).
- **Claim C4.3:** Security response activities are coordinated by the maintainer council.  
  *Evidence:* [GOVERNANCE.md#security-response-team](../GOVERNANCE.md#security-response-team).

### Strategy S5 – Transparency and Continuous Improvement Enable External Assurance
- **Claim C5.1:** Public documentation and examples clarify expected usage and configurations.  
  *Evidence:* [Project site](https://project-dalec.github.io/dalec/), [Examples gallery](https://project-dalec.github.io/dalec/examples).
- **Claim C5.2:** External scorecards and best-practice badges track adherence to industry standards.  
  *Evidence:* [README.md#badges](../README.md#badges).
- **Claim C5.3:** Updates to this assurance case and other governance documents follow the public pull-request workflow.  
  *Evidence:* [GOVERNANCE.md#values](../GOVERNANCE.md#values), [GOVERNANCE.md#code-changes](../GOVERNANCE.md#code-changes) (open decision making and PR approval requirements).

## Evidence Summary
| ID | Claim | Evidence Reference |
| --- | --- | --- |
| C1.1 | Governance values | [GOVERNANCE.md#values](../GOVERNANCE.md#values), [CONTRIBUTOR_LADDER.md](../CONTRIBUTOR_LADDER.md) |
| C1.2 | Security response ownership | [GOVERNANCE.md#security-response-team](../GOVERNANCE.md#security-response-team) |
| C2.1 | CLA & DCO enforcement | [README.md#contributing](../README.md#contributing), [CONTRIBUTING.md#contributing-a-patch](../CONTRIBUTING.md#contributing-a-patch) |
| C2.2 | Branch protections | [GOVERNANCE.md#branch-protection](../GOVERNANCE.md#branch-protection), [GOVERNANCE.md#code-changes](../GOVERNANCE.md#code-changes) |
| C2.3 | Secure MFA enforcement | [SECURITY.md#maintainer-authentication](../SECURITY.md#maintainer-authentication) |
| C2.4 | Static analysis in CI | [.github/workflows/ci.yml](../.github/workflows/ci.yml), [.github/workflows/codeql.yml](../.github/workflows/codeql.yml) |
| C2.5 | Architectural documentation | [ARCHITECTURE.md](../ARCHITECTURE.md) |
| C2.6 | Secret management practices | [SECURITY.md#secret-management](../SECURITY.md#secret-management), [Signing secrets guide](https://project-dalec.github.io/dalec/signing#secrets) |
| C3.1 | Declarative BuildKit orchestration | [ARCHITECTURE.md#build-orchestration-flow](../ARCHITECTURE.md#build-orchestration-flow), [ARCHITECTURE.md#runtime-components](../ARCHITECTURE.md#runtime-components), [.github/workflows/release.yml](../.github/workflows/release.yml) |
| C3.2 | Generation validation controls | [.github/workflows/ci.yml](../.github/workflows/ci.yml), [spec.go](../spec.go) |
| C3.3 | Release pipeline controls | [.github/workflows/release.yml](../.github/workflows/release.yml), [.github/workflows/frontend-image.yml](../.github/workflows/frontend-image.yml), [GOVERNANCE.md#branch-protection](../GOVERNANCE.md#branch-protection) |
| C3.4 | Dependency monitoring | [.github/dependabot.yml](../.github/dependabot.yml), [.github/workflows/dependency-review.yml](../.github/workflows/dependency-review.yml), [SECURITY.md#dependency-monitoring](../SECURITY.md#dependency-monitoring) |
| C4.1 | Private disclosure process | [SECURITY.md#reporting-security-issues](../SECURITY.md#reporting-security-issues) |
| C4.2 | Post-remediation advisories | [SECURITY.md#communication](../SECURITY.md#communication) |
| C4.3 | Maintainer-led incident response | [GOVERNANCE.md#security-response-team](../GOVERNANCE.md#security-response-team) |
| C5.1 | Public docs & examples | [Project site](https://project-dalec.github.io/dalec/), [Examples gallery](https://project-dalec.github.io/dalec/examples) |
| C5.2 | External assurance signals | [README.md#badges](../README.md#badges) |
| C5.3 | Open change management | [GOVERNANCE.md#values](../GOVERNANCE.md#values), [GOVERNANCE.md#code-changes](../GOVERNANCE.md#code-changes) |

## Maintenance and Review
- **Update cadence:** Review and refresh this assurance case at least once per release cycle or quarterly, whichever comes first.
- **Responsibility:** [Dalec Maintainers](../MAINTAINERS.md#project-dalec-maintainers), using the standard pull-request workflow.
- **Change tracking:** Each edit must reference any new evidence (workflow changes or policy updates) to preserve traceability.

## Residual Risks and Planned Enhancements
- Official release artifacts currently publish unsigned container images without accompanying SBOM or provenance attestations. The maintainer council tracks this gap and plans to extend the release workflow to emit signed, attestable outputs.
