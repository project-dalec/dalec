package test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/platforms"
	"github.com/cpuguy83/ocijoin"
	"github.com/distribution/reference"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/v3/assert"
)

const (
	// platformMarkerStoreID is the id used to register the OCI layout store with
	// the build session and to reference it from a build context or source policy.
	platformMarkerStoreID = "dalec-worker-platform-test"
	// platformMarkerPath is the file injected into each worker image whose
	// contents identify which platform-specific manifest was selected.
	platformMarkerPath = "/platform-marker"
)

// buildWorkerMarkerStore builds an OCI layout content store containing a
// multi-platform index derived from the real distro worker base image
// (baseRef), with one manifest per provided platform. Each manifest is the real
// base image for that platform plus an injected marker file
// ([platformMarkerPath]) whose contents are the formatted platform string
// ([platformMarkerString]).
//
// Using the real base image (rather than a synthetic one) means the resulting
// worker image is a functional distro image, so it works both when supplied as a
// worker context (which short-circuits worker building) and when the normal
// worker build runs dnf install on it. The injected marker file lets a test
// verify which platform-specific manifest was selected for a requested build
// platform.
//
// It returns the store id (for use with [testenv.WithOCIStore],
// [withOCILayoutContext], and [workerRewriteSourcePolicy]), the digest of the
// index, and the content store.
func buildWorkerMarkerStore(ctx context.Context, t *testing.T, baseRef string, plats ...ocispecs.Platform) (storeID string, indexDigest digest.Digest, store content.Store) {
	t.Helper()

	bk, err := testEnv.Buildkit(ctx)
	assert.NilError(t, err)

	dir := t.TempDir()

	build := func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		res := gwclient.NewResult()
		expPlatforms := &exptypes.Platforms{Platforms: make([]exptypes.Platform, 0, len(plats))}

		for _, p := range plats {
			p := p
			pk := platforms.Format(p)

			st := llb.Image(baseRef, llb.Platform(p)).
				File(llb.Mkfile(platformMarkerPath, 0o644, []byte(platformMarkerString(p))))

			def, err := st.Marshal(ctx, llb.Platform(p))
			if err != nil {
				return nil, err
			}

			r, err := c.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
			if err != nil {
				return nil, err
			}

			ref, err := r.SingleRef()
			if err != nil {
				return nil, err
			}
			res.AddRef(pk, ref)

			// Provide the real base image config (which carries the correct
			// os/architecture) so the exported manifest is tagged with the right
			// platform. The exporter reconciles rootfs diff_ids with the actual
			// layers, so the injected marker layer is included automatically.
			_, _, cfgDt, err := c.ResolveImageConfig(ctx, baseRef, sourceresolver.Opt{
				ImageOpt: &sourceresolver.ResolveImageOpt{Platform: &p},
			})
			if err != nil {
				return nil, err
			}
			res.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, pk), cfgDt)

			expPlatforms.Platforms = append(expPlatforms.Platforms, exptypes.Platform{ID: pk, Platform: p})
		}

		dt, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		res.AddMeta(exptypes.ExporterPlatformsKey, dt)
		return res, nil
	}

	so := client.SolveOpt{
		Exports: []client.ExportEntry{{
			Type:      client.ExporterOCI,
			OutputDir: dir,
			Attrs:     map[string]string{"tar": "false"},
		}},
	}

	statusCh := make(chan *client.SolveStatus)
	go func() {
		for range statusCh {
		}
	}()

	_, err = bk.Build(ctx, so, "", build, statusCh)
	assert.NilError(t, err)

	// The BuildKit OCI exporter writes the multi-platform index to index.json
	// (not as an addressable blob) and may include attestation manifests. Use
	// ocijoin to resolve any nested index, drop non-platform manifests, and
	// re-nest the platform index as an addressable blob so it can be referenced by
	// digest from a build context or source policy.
	layout, err := ocijoin.NewLocalLayout(dir)
	assert.NilError(t, err)

	keep := func(d ocispecs.Descriptor) bool {
		if ocijoin.IsAttestation(d) {
			return false
		}
		return d.Platform != nil && d.Platform.OS != "unknown" && d.Platform.Architecture != "unknown"
	}
	wrapped := ocijoin.Wrap(ocijoin.Filter(ocijoin.Unwrap(layout), keep), nil)

	outer, err := wrapped.Index(ctx)
	assert.NilError(t, err)
	assert.Assert(t, len(outer.Manifests) == 1, "expected wrapped layout to nest a single platform index")
	indexDigest = outer.Manifests[0].Digest

	// A Layout is already a content.Provider; layoutStore adapts it to the
	// content.Store that BuildKit's OCIStores expects, without copying blobs to
	// an on-disk store.
	return platformMarkerStoreID, indexDigest, &layoutStore{layout: wrapped}
}

// layoutStore adapts an ocijoin.Layout (a read-only content.Provider) into the
// containerd content.Store that BuildKit's SolveOpt.OCIStores requires.
//
// BuildKit's oci-layout source only exercises the read path: ReaderAt to fetch
// blobs and Info to size the root descriptor. Every other content.Store method
// is promoted from the embedded nil content.Store; none are called on the read
// path and would panic if they were.
type layoutStore struct {
	content.Store // nil: unimplemented methods, never called on the read path
	layout        ocijoin.Layout
}

func (s *layoutStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	return s.layout.ReaderAt(ctx, desc)
}

// Info reports the digest and size of a blob. ReaderAt only requires the digest
// to be set, so the size is read from the returned reader.
func (s *layoutStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	ra, err := s.layout.ReaderAt(ctx, ocispecs.Descriptor{Digest: dgst})
	if err != nil {
		return content.Info{}, err
	}
	defer func() { _ = ra.Close() }()
	return content.Info{Digest: dgst, Size: ra.Size()}, nil
}

// workerRewriteSourcePolicy returns a source policy that rewrites the distro
// worker base image (baseRef, resolved via the llb.Image path as
// "docker-image://<baseRef>") to the multi-platform marker index in the OCI
// layout store (storeID) at indexDigest.
//
// This exercises the default llb.Image worker-resolution path (unlike the worker
// context override, which short-circuits worker building). BuildKit performs
// platform-specific manifest selection against the rewritten oci-layout source
// using the source op's platform, so the resulting worker carries the marker for
// the requested build platform. The store id is supplied via the oci.store attr;
// the session id is left empty so BuildKit resolves the store from the current
// build session.
func workerRewriteSourcePolicy(baseRef, storeID string, indexDigest digest.Digest) *spb.Policy {
	// The oci-layout source identifier is parsed as an image reference, which
	// requires a normalized (domain-qualified) name; a bare single-component name
	// makes the digest parse as a port. Normalize the store id for the identifier
	// while keeping the raw store id in the oci.store attr so it matches the store
	// registered with the session.
	named, err := reference.ParseNormalizedNamed(storeID)
	if err != nil {
		panic(err)
	}
	dgstRef, err := reference.WithDigest(named, indexDigest)
	if err != nil {
		panic(err)
	}

	return &spb.Policy{
		Rules: []*spb.Rule{{
			Action: spb.PolicyAction_CONVERT,
			Selector: &spb.Selector{
				MatchType:  spb.MatchType_WILDCARD,
				Identifier: "docker-image://" + baseRef + "*",
			},
			Updates: &spb.Update{
				Identifier: "oci-layout://" + dgstRef.String(),
				// An oci-layout source has two parts: the identifier
				// (oci-layout://<name>@<digest>) says *what* to fetch by digest,
				// while pb.AttrOCILayoutStoreID ("oci.store") says *where* to fetch
				// it from. Its value is the id of the content store registered with
				// the session via SolveOpt.OCIStores (testenv.WithOCIStore here), so
				// it must equal that registration key. Unlike the identifier name it
				// is matched literally, not parsed as an image ref, so it keeps the
				// raw storeID. The sibling pb.AttrOCILayoutSessionID ("oci.session")
				// is intentionally omitted: leaving it empty makes BuildKit's
				// oci-layout resolver fall back to any store in the current build
				// session, which is exactly the store we just registered.
				Attrs: map[string]string{
					pb.AttrOCILayoutStoreID: storeID,
				},
			},
		}},
	}
}

// platformMarkerString is the marker contents for a platform image, also used
// by tests as the expected value when that image is selected.
func platformMarkerString(p ocispecs.Platform) string {
	s := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		s += "/" + p.Variant
	}
	return s
}
