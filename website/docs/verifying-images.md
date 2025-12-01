# Verifying Release Images

All official Dalec container images are cryptographically signed and include attestations for build provenance. This guide explains how to verify you're using authentic, unmodified Dalec releases.

## When to Verify

**You should verify Dalec images before using them to build your packages**, especially if:

- You're using Dalec in production or CI/CD pipelines
- Your organization has security policies requiring verified software
- You want to ensure you're not using a compromised version of Dalec
- You need an audit trail showing what versions of Dalec were used

**How Dalec images are used:**
- When you run `docker build` with a Dalec spec, Docker automatically pulls the Dalec frontend image (`ghcr.io/project-dalec/dalec/frontend`) to process your build
- You typically don't interact with these images directly - Docker handles them behind the scenes
- **However**, you should verify them before your first use or when updating to a new version

## Why Verify Images?

Image verification provides several security benefits:

- **Authenticity**: Confirm the image was published by the official Dalec repository, not an attacker
- **Integrity**: Detect if the image has been tampered with after publishing
- **Provenance**: Verify the image was built from the expected source code and official workflow
- **Supply Chain Security**: Meet security best practices and compliance requirements (e.g., SLSA framework)
- **Audit Trail**: Document which verified versions of Dalec were used to build your packages

## Quick Start

Here's a simple workflow for verifying Dalec before using it:

```bash
# 1. Install cosign (one-time setup)
brew install cosign  # macOS

# 2. Before using Dalec, verify the frontend image
cosign verify ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0

# 3. If verification succeeds, you can safely use Dalec
docker build -f my-spec.yml --target mariner2/rpm .
```

If verification fails, **do not use that image** - it may be compromised or unofficial.

## What Gets Signed

The following container images are signed when published as releases:

- **Frontend image**: `ghcr.io/project-dalec/dalec/frontend` - The main BuildKit frontend that processes your Dalec spec files
- **Worker images**: Target-specific images like `ghcr.io/project-dalec/dalec/noble/worker` - Used internally by the frontend for specific Linux distributions

**Note**: Docker pulls these images automatically when you run builds. You don't reference them directly in your Dockerfile, but you should verify them before first use.

Images are signed using [Sigstore's cosign](https://github.com/sigstore/cosign) with keyless signing via GitHub OIDC tokens.

## Prerequisites

To verify images, you can use either:

### Option 1: Cosign (Recommended)

```bash
# Install cosign (see https://docs.sigstore.dev/cosign/installation for more options)
brew install cosign  # macOS
# or
ARCH=$(uname -m)
curl -O -L "https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-${ARCH}"
sudo mv cosign-linux-${ARCH} /usr/local/bin/cosign
sudo chmod +x /usr/local/bin/cosign
```

### Option 2: GitHub CLI

If you have the [GitHub CLI](https://cli.github.com/) installed (v2.49.0+), you can use the built-in attestation verification:

```bash
gh attestation verify oci://ghcr.io/project-dalec/dalec/frontend:v0.19.0 --owner project-dalec
```

This guide primarily uses cosign for detailed examples, but GitHub CLI is simpler for basic verification.

## Verifying Image Signatures

### Verify Frontend Image

To verify a specific release of the frontend image:

```bash
cosign verify ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0
```

To verify the latest release:

```bash
cosign verify ghcr.io/project-dalec/dalec/frontend:latest \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v.*'
```

### Verify Worker Images

Worker images follow a similar pattern:

```bash
cosign verify ghcr.io/project-dalec/dalec/noble/worker:v0.9 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/worker-images.yml@refs/tags/v0.19.0
```

### Understanding the Output

A successful verification will output JSON containing the signature details:

```json
[
  {
    "critical": {
      "identity": {
        "docker-reference": "ghcr.io/project-dalec/dalec/frontend"
      },
      "image": {
        "docker-manifest-digest": "sha256:abc123..."
      },
      "type": "cosign container image signature"
    },
    "optional": {
      "Bundle": {
        "SignedEntryTimestamp": "...",
        "Payload": {
          "body": "...",
          "integratedTime": 1234567890,
          "logIndex": 12345678,
          "logID": "..."
        }
      }
    }
  }
]
```

If verification fails, you'll see an error message such as:

```
Error: no matching signatures
```

This could mean:
- The image hasn't been signed (e.g., development builds)
- The image has been tampered with
- The image was built by a different workflow or repository

## Verifying Build Provenance

In addition to signatures, release images include SLSA provenance attestations that document how the image was built.

### View Provenance Attestation

```bash
# View build provenance attestation for an image
cosign verify-attestation ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0 \
  --type https://slsa.dev/provenance/v1 | jq
```

The provenance includes:
- Source repository and commit SHA
- GitHub Actions workflow that built the image
- Build parameters and environment
- Timestamps and builder information

You can also use the GitHub CLI to view attestations:

```bash
gh attestation verify oci://ghcr.io/project-dalec/dalec/frontend:v0.19.0 --owner project-dalec
```

### Viewing SBOM (Software Bill of Materials)

Images built with the `docker/build-push-action` include an SBOM attestation that lists all software components:

```bash
# View SBOM attestation
cosign verify-attestation ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0 \
  --type https://spdx.dev/Document | jq
```

Alternatively, you can use Docker's built-in SBOM viewer:

```bash
docker buildx imagetools inspect ghcr.io/project-dalec/dalec/frontend:v0.19.0 --format '{{json .}}' | jq '.sbom'
```

## Automated Verification in CI/CD

For production use, you should verify Dalec images in your CI/CD pipeline before building packages. This ensures every build uses verified, official Dalec releases.

### GitHub Actions Example

```yaml
jobs:
  build-packages:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Install Cosign
        uses: sigstore/cosign-installer@v3.7.0
      
      - name: Verify Dalec Frontend Image
        run: |
          # Verify the Dalec version you're about to use
          cosign verify ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com \
            --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0
          
          echo "Dalec frontend image verified successfully"
      
      - name: Build Package with Dalec
        run: |
          # Now safely use Dalec to build your package
          docker build -f my-package.yml --target mariner2/rpm -o ./output .
```

### GitLab CI Example

```yaml
verify-dalec:
  stage: verify
  image: alpine:latest
  before_script:
    - apk add --no-cache cosign
  script:
    - |
      cosign verify ghcr.io/project-dalec/dalec/frontend:v0.19.0 \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com \
        --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.19.0
  
build-package:
  stage: build
  needs: [verify-dalec]
  script:
    - docker build -f my-package.yml --target mariner2/rpm -o ./output .
```

### Best Practices

1. **Pin versions**: Always specify exact version tags (e.g., `v0.19.0`) rather than `latest`
2. **Verify early**: Add verification as one of the first steps in your pipeline
3. **Fail fast**: If verification fails, stop the pipeline immediately
4. **Audit logs**: Save verification results for compliance and debugging
5. **Update carefully**: When updating Dalec versions, verify the new version before deploying

## Development vs. Release Images

**Important**: Only tagged release images are signed. Images built from:
- Pull requests
- Branch commits (including `main`)
- Local development builds

...are **NOT signed** and verification will fail. This is intentional to ensure only official releases are signed.

## Additional Resources

For more information about image signing and verification:

- **Troubleshooting verification issues**: See the [Cosign verify documentation](https://docs.sigstore.dev/cosign/verifying/verify/)
- **Understanding keyless signing**: See the [Sigstore keyless signing overview](https://docs.sigstore.dev/signing/overview/)
- **Signing packages with Dalec**: See [Signing Packages](./signing.md) for signing RPM/DEB packages built with Dalec
- **SLSA provenance**: Learn more about [SLSA Framework](https://slsa.dev/) and supply chain security
