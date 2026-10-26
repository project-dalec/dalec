package test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"path"
	"regexp"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/v3/assert"
)

func testWorkerCADistrust(t *testing.T, target targetConfig) {
	t.Run("workers do not trust emSign certs", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(baseCtx, t)

		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(arch, func(t *testing.T) {
				t.Parallel()

				ctx := startTestSpan(ctx, t)
				testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
					platform := ocispecs.Platform{OS: "linux", Architecture: arch}
					req := newSolveRequest(withSpec(ctx, t, nil), withBuildTarget(target.Worker), withPlatform(platform))
					res := solveT(ctx, t, client, req)
					ref, err := res.SingleRef()
					assert.NilError(t, err)

					bundle := readWorkerCertificates(ctx, t, ref, "/etc/ssl/certs/ca-certificates.crt")
					entries, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{Path: "/etc/ssl/certs"})
					assert.NilError(t, err)

					hashName := regexp.MustCompile(`^[0-9a-f]{8}\.[0-9]+$`)
					var hashed []*x509.Certificate
					for _, entry := range entries {
						if hashName.MatchString(path.Base(entry.Path)) {
							hashed = append(hashed, readWorkerCertificates(ctx, t, ref, path.Join("/etc/ssl/certs", path.Base(entry.Path)))...)
						}
					}
					assert.Assert(t, len(hashed) != 0, "empty hashed certificate store")

					for name, certs := range map[string][]*x509.Certificate{"bundle": bundle, "hashed store": hashed} {
						for _, cert := range certs {
							for _, distrusted := range []string{"emSign ECC Root CA - G3", "AAA Certificate Services"} {
								assert.Assert(t, cert.Subject.CommonName != distrusted, "%s still trusts %s", name, distrusted)
							}
						}
					}
				})
			})
		}
	})
}

func readWorkerCertificates(ctx context.Context, t *testing.T, ref gwclient.Reference, filename string) []*x509.Certificate {
	t.Helper()

	data, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: filename})
	assert.NilError(t, err, filename)

	var certs []*x509.Certificate
	for len(strings.TrimSpace(string(data))) != 0 {
		block, rest := pem.Decode(data)
		assert.Assert(t, block != nil, "invalid PEM in %s", filename)
		assert.Equal(t, block.Type, "CERTIFICATE", filename)
		cert, err := x509.ParseCertificate(block.Bytes)
		assert.NilError(t, err, filename)

		certs = append(certs, cert)
		data = rest
	}
	assert.Assert(t, len(certs) != 0, "no certificates in %s", filename)

	return certs
}
