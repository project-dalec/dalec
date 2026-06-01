# Security Policy

## Supported Versions

Dalec remains in the process of getting to a stable v1.0 release, and as such does not currently provide a long-term supported version.
We make a good faith effort to respond to security issues in a timely manner and will release version updates as needed to address them.
Users should expect to upgrade to the latest release version to stay current on security updates.

## Communication

We will publish known vulnerabilities through a [GitHub Security Advisory](https://github.com/project-dalec/dalec/security/advisories) once they have been addressed to inform the community of their potential scope, impact, and mitigation.

## Reporting Security Issues

Project Dalec and its maintainers take the security of the project seriously, and we appreciate your efforts to responsibly disclose your findings to us.

> **Please do not report security vulnerabilities through public GitHub issues.**

Instead, please report them through our [private vulnerability reporting](https://github.com/project-dalec/dalec/security/advisories/new) form.

Please include the requested information listed below (as much as you can provide) to help us better understand the nature and scope of the possible issue:

* Type of issue (e.g. buffer overflow, SQL injection, cross-site scripting, etc.)
* Full paths of source file(s) related to the manifestation of the issue
* The location of the affected source code (tag/branch/commit or direct URL)
* Any special configuration required to reproduce the issue
* Step-by-step instructions to reproduce the issue
* Proof-of-concept or exploit code (if possible)
* Impact of the issue, including how an attacker might exploit the issue

This information will help us triage your report more quickly.

We believe in [Coordinated Vulnerability Disclosure (CVD)](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure) and will work with you through the private advisory report.

## Preferred Languages

We prefer all communications to be in English.

## Maintainer Authentication

All members of the Project Dalec GitHub organization must authenticate using secure multi-factor mechanisms (for example, FIDO2 hardware security keys such as YubiKey devices). GitHub enforces this policy at the organization level and disallows SMS or email-based second factors for members; accounts without a compliant hardware or app-based (TOTP, WebAuthn) factor cannot access project resources.

## Dependency Monitoring

Automated scanners in GitHub monitor direct and transitive dependencies. Dependabot continuously proposes security updates based on `dependabot.yml`, and each pull request it opens is reviewed and merged by a maintainer once tests pass. The GitHub Dependency Review workflow annotates contributor pull requests with vulnerability findings so reviewers can block risky upgrades before merge. The Snyk GitHub App integration scans release branches and raises alerts in the repository security dashboard, where maintainers triage and remediate advisories.

## Secret Management

The project does not maintain long-lived shared secrets in source control or CI. Workflow automation relies only on the GitHub-provided `GITHUB_TOKEN`, which GitHub scopes automatically: in `ci.yml` it has minimal read/package permissions for pull-request and branch builds, and in release workflows it receives package-publish access only when a signed tag triggers the job. Maintainers monitor the default token permissions and avoid storing additional credentials in repository settings.
