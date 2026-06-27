package distro

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/test"
)

func TestBuildContainerMinimizesRPMRootfs(t *testing.T) {
	t.Parallel()

	c := &Config{
		ImageRef:        "worker-image",
		ReleaseVer:      "1",
		BuilderPackages: []string{"rpm"},
		BasePackages: []dalec.Spec{
			{
				Name:        "dalec-base-test",
				Version:     "0.0.1",
				Revision:    "1",
				License:     "Apache-2.0",
				Description: "base package",
			},
		},
		InstallFunc: DnfInstall,
	}

	sOpt := dalec.SourceOpts{
		GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
			return nil, nil
		},
	}

	state := c.BuildContainer(t.Context(), &testClient{}, sOpt, &dalec.Spec{}, "target", rpmFixture())

	ops, err := test.LLBOpsFromState(t.Context(), state)
	if err != nil {
		t.Fatalf("failed to get llb ops from state: %v", err)
	}

	assertProgressGroup(t, ops, "Minimize RPM container", true)
	assertProgressGroup(t, ops, "Squash RPM container", true)
}

func TestBuildContainerSkipsRPMMinimizationForCustomBase(t *testing.T) {
	t.Parallel()

	c := &Config{
		ImageRef:        "worker-image",
		ReleaseVer:      "1",
		BuilderPackages: []string{"rpm"},
		InstallFunc:     DnfInstall,
	}

	sOpt := dalec.SourceOpts{
		GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
			return nil, nil
		},
	}

	spec := &dalec.Spec{
		Image: &dalec.ImageConfig{
			Bases: []dalec.BaseImage{
				{
					Rootfs: dalec.Source{
						DockerImage: &dalec.SourceDockerImage{
							Ref: "custom-base",
						},
					},
				},
			},
		},
	}

	state := c.BuildContainer(t.Context(), &testClient{}, sOpt, spec, "target", rpmFixture())

	ops, err := test.LLBOpsFromState(t.Context(), state)
	if err != nil {
		t.Fatalf("failed to get llb ops from state: %v", err)
	}

	assertProgressGroup(t, ops, "Minimize RPM container", false)
	assertProgressGroup(t, ops, "Squash RPM container", false)
}

func TestRPMMinimizeScriptPreservesScannerUsableDB(t *testing.T) {
	t.Parallel()

	required := []string{
		`rpm --root "${rootfs}" -e --noscripts --notriggers --nodeps`,
		`%{NAME}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\n`,
		`"${arch}" = "(none)"`,
		`ln -s ../../usr/lib/sysimage/rpm "${rootfs}/var/lib/rpm"`,
		`rpm_root -qa >/dev/null`,
		`%{REQUIREFLAGS:deptype}`,
		`required RPM dependency ${req} has no installed provider`,
		`failed to read RPM requirements for ${pkg}`,
		`is_scriptlet_requirement`,
	}

	for _, s := range required {
		if !strings.Contains(rpmMinimizeScript, s) {
			t.Fatalf("expected rpm minimization script to contain %q", s)
		}
	}
}

func TestRPMMinimizeScriptSyntax(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}

	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(rpmMinimizeScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rpm minimization script has invalid bash syntax: %v\n%s", err, out)
	}
}

func rpmFixture() llb.State {
	return llb.Scratch().
		File(llb.Mkdir("/RPMS/x86_64", 0o755)).
		File(llb.Mkfile("/RPMS/x86_64/example.rpm", 0o644, []byte("not an rpm")))
}

func assertProgressGroup(t *testing.T, ops []test.LLBOp, name string, want bool) {
	t.Helper()

	for _, op := range ops {
		if op.OpMetadata.ProgressGroup.Name == name {
			if !want {
				t.Fatalf("did not expect progress group %q", name)
			}
			return
		}
	}

	if want {
		t.Fatalf("expected progress group %q", name)
	}
}

// testClient implements client.Client for LLB graph tests.
type testClient struct {
	buildOpts client.BuildOpts
}

func (tc *testClient) BuildOpts() client.BuildOpts {
	return tc.buildOpts
}

func (tc *testClient) Solve(context.Context, client.SolveRequest) (*client.Result, error) {
	return nil, nil
}

func (tc *testClient) Inputs(context.Context) (map[string]llb.State, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) NewContainer(context.Context, client.NewContainerRequest) (client.Container, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) ResolveImageConfig(context.Context, string, sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("not implemented")
}

func (tc *testClient) ResolveSourceMetadata(context.Context, *pb.SourceOp, sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) Warn(context.Context, digest.Digest, string, client.WarnOpts) error {
	return nil
}
