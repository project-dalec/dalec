# Verifying Release Images

All official Dalec container images published to GitHub Container Registry (GHCR) are cryptographically signed and include attestations for build provenance. This enables users to verify that images are authentic and haven't been tampered with.

## Why Verify Images?

Image verification provides several security benefits:

- **Authenticity**: Confirm the image was published by the official Dalec repository
- **Integrity**: Detect if the image has been tampered with or modified
- **Provenance**: Verify the image was built from the expected source code and workflow
- **Supply Chain Security**: Comply with security best practices and policies (e.g., SLSA framework)

## What Gets Signed

The following container images are signed when published as releases:

- **Frontend image**: `ghcr.io/project-dalec/dalec/frontend` (main BuildKit frontend)
- **Worker images**: Various target-specific worker images (e.g., `ghcr.io/project-dalec/dalec/mariner2/container`)

Images are signed using [Sigstore's cosign](https://github.com/sigstore/cosign) with keyless signing via GitHub OIDC tokens.

## Prerequisites

To verify images, you can use either:

### Option 1: Cosign (Recommended)

```bash
# Install cosign (see https://docs.sigstore.dev/cosign/installation for more options)
brew install cosign  # macOS
# or
curl -O -L "https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-amd64"
sudo mv cosign-linux-amd64 /usr/local/bin/cosign
sudo chmod +x /usr/local/bin/cosign
```

### Option 2: GitHub CLI

If you have the [GitHub CLI](https://cli.github.com/) installed (v2.49.0+), you can use the built-in attestation verification:

```bash
gh attestation verify oci://ghcr.io/project-dalec/dalec/frontend:v0.9.0 --owner project-dalec
```

This guide primarily uses cosign for detailed examples, but GitHub CLI is simpler for basic verification.

## Verifying Image Signatures

### Verify Frontend Image

To verify a specific release of the frontend image:

```bash
cosign verify ghcr.io/project-dalec/dalec/frontend:v0.9.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.9.0
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
cosign verify ghcr.io/project-dalec/dalec/mariner2/container:v0.9 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/worker-images.yml@refs/tags/v0.9.0
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
cosign verify-attestation ghcr.io/project-dalec/dalec/frontend:v0.9.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.9.0 \
  --type https://slsa.dev/provenance/v1 | jq
```

The provenance includes:
- Source repository and commit SHA
- GitHub Actions workflow that built the image
- Build parameters and environment
- Timestamps and builder information

You can also use the GitHub CLI to view attestations:

```bash
gh attestation verify oci://ghcr.io/project-dalec/dalec/frontend:v0.9.0 --owner project-dalec
```

### Viewing SBOM (Software Bill of Materials)

Images built with the `docker/build-push-action` include an SBOM attestation that lists all software components:

```bash
# View SBOM attestation
cosign verify-attestation ghcr.io/project-dalec/dalec/frontend:v0.9.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v0.9.0 \
  --type https://spdx.dev/Document | jq
```

Alternatively, you can use Docker's built-in SBOM viewer:

```bash
docker buildx imagetools inspect ghcr.io/project-dalec/dalec/frontend:v0.9.0 --format '{{json .}}' | jq '.sbom'
```

Or use GitHub CLI:

```bash
gh attestation verify oci://ghcr.io/project-dalec/dalec/frontend:v0.9.0 --owner project-dalec --format json | jq '.attestations[] | select(.predicateType | contains("spdx"))'
```

## Automated Verification

### In CI/CD Pipelines

You can add verification steps to your CI/CD pipelines:

```yaml
- name: Verify Dalec Frontend Image
  run: |
    cosign verify ghcr.io/project-dalec/dalec/frontend:v0.9.0 \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com \
      --certificate-identity-regexp 'https://github.com/project-dalec/dalec/.github/workflows/.*'
```

### Using Admission Controllers

For Kubernetes deployments, you can use admission controllers like:

- [Sigstore Policy Controller](https://docs.sigstore.dev/policy-controller/overview/)
- [Kyverno](https://kyverno.io/) with cosign verification
- [Open Policy Agent (OPA) Gatekeeper](https://www.openpolicyagent.org/docs/latest/kubernetes-introduction/)

These can automatically reject unsigned or unverified images.

## Development vs. Release Images

**Important**: Only tagged release images are signed. Images built from:
- Pull requests
- Branch commits (including `main`)
- Local development builds

...are **NOT signed** and verification will fail. This is intentional to ensure only official releases are signed.

## Troubleshooting

### "Error: no matching signatures"

This error means no valid signature was found. Common causes:

1. **Development image**: You're trying to verify a non-release image (only tagged releases are signed)
2. **Wrong identity**: The `--certificate-identity` doesn't match the workflow that built the image
   - For frontend images, use: `https://github.com/project-dalec/dalec/.github/workflows/frontend-image.yml@refs/tags/v*`
   - For worker images, use: `https://github.com/project-dalec/dalec/.github/workflows/worker-images.yml@refs/tags/v*`
   - Make sure the tag matches the image tag you're verifying
3. **Wrong issuer**: Make sure you're using `--certificate-oidc-issuer https://token.actions.githubusercontent.com`
4. **Tampered image**: The image has been modified after signing (security issue!)

### "Error: failed to verify signature"

This indicates the signature exists but verification failed. Possible causes:

1. **Clock skew**: System time is significantly off
2. **Network issues**: Cannot reach Sigstore infrastructure (rekor.sigstore.dev, fulcio.sigstore.dev)
3. **Revoked certificate**: The signing certificate was revoked (rare)

### Getting Help

If you encounter issues with image verification:

1. Check the [Dalec Releases](https://github.com/project-dalec/dalec/releases) page to confirm the version was signed
2. Verify your cosign installation: `cosign version`
3. Open an issue at [github.com/project-dalec/dalec/issues](https://github.com/project-dalec/dalec/issues)

## Technical Details

### Signing Process

Images are signed during the GitHub Actions release workflow using:

1. **Cosign**: Signs images with keyless OIDC signing (no private keys stored)
2. **GitHub OIDC token**: Provides identity binding to the repository and workflow
3. **Sigstore Fulcio**: Issues short-lived certificates
4. **Sigstore Rekor**: Records signatures in a transparency log

### Keyless Signing

Dalec uses "keyless" signing, meaning:
- No private keys are stored or managed
- Signatures are tied to the GitHub repository identity
- Certificates are short-lived (10 minutes)
- All signatures are recorded in a public transparency log
- Verification relies on the certificate identity, not a public key

This approach provides strong security without the operational burden of key management.

## Related Documentation

- [Signing Packages](./signing.md) - How to sign RPM/DEB packages built with Dalec
- [Cosign Documentation](https://docs.sigstore.dev/cosign/overview/)
- [SLSA Framework](https://slsa.dev/)
- [GitHub Actions Security Hardening](https://docs.github.com/en/actions/security-guides/security-hardening-for-github-actions)
