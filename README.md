# Dalec

Dalec is a project aimed at providing a declarative format for building system packages and containers from those packages.

Our goal is to provide a secure way to build packages and containers, with a focus on supply chain security.

## Features

- 🐳 No additional tools are needed except for [Docker](https://docs.docker.com/engine/install/)!
- 🚀 Easy to use declarative configuration
- 📦 Build packages and/or containers for a number of different [targets](https://project-dalec.github.io/dalec/targets)
  - DEB-based: Debian, and Ubuntu
  - RPM-based: Azure Linux, Rocky Linux, and Alma Linux
  - Windows containers (cross compilation only)
- 🔌 Pluggable support for other operating systems
- 🤏 Minimal image size, resulting in less vulnerabilities and smaller attack surface
- 🪟 Support for Windows containers
- ✍️ Support for signed packages
- 🔐 Ensure supply chain security with build time SBOMs, and Provenance attestations

👉 To get started, please see [Dalec documentation](https://project-dalec.github.io/dalec/)!

## Contributing

This project welcomes contributions and suggestions. Dalec uses the [Developer Certificate of Origin (DCO)](https://wiki.linuxfoundation.org/dco) to confirm authorship and licensing intent.
Each commit must include a Signed-off-by line; run `git commit -s` to add it automatically.
The CNCF-operated `dco-2` GitHub App enforces this requirement on every pull request.
See [CONTRIBUTING.md](https://github.com/project-dalec/dalec/blob/main/CONTRIBUTING.md#contributing-a-patch) for additional guidance.

Dalec has adopted the CNCF Code of Conduct. Refer to our [Community Code of Conduct](https://github.com/project-dalec/dalec/blob/main/CODE_OF_CONDUCT.md) for details.
For more information, see the [CNCF Code of Conduct FAQ](https://github.com/cncf/foundation/blob/main/code-of-conduct-faq.md) or contact conduct@cncf.io with any additional questions or comments.

### Badges

[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/10703/badge)](https://www.bestpractices.dev/projects/10703)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/project-dalec/dalec/badge)](https://scorecard.dev/viewer/?uri=github.com/project-dalec/dalec)

Copyright Contributors to Dalec, established as Dalec a Series of LF Projects, LLC.
