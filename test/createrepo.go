package test

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

func createYumRepo(installer *distro.Config) func(rpms llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
	return func(rpms llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
		return func(in llb.State) llb.State {
			suffixBytes := sha256.Sum256([]byte(repoPath))
			suffix := hex.EncodeToString(suffixBytes[:])[:8]
			localRepo := []byte(`
[Local-` + suffix + `]
name=Local Repository
baseurl=file://` + repoPath + `
gpgcheck=0
priority=0
enabled=1
metadata_expire=0
`)

			pg := dalec.ProgressGroup("Install local repo for test")

			installOpts := []distro.DnfInstallOpt{
				distro.DnfInstallWithConstraints([]llb.ConstraintsOpt{pg}),
			}

			withRepos := in.
				Run(installer.Install([]string{"createrepo"}, installOpts...), pg).
				File(llb.Mkdir(filepath.Join(repoPath, "RPMS"), 0o755, llb.WithParents(true)), pg).
				File(llb.Mkdir(filepath.Join(repoPath, "SRPMS"), 0o755), pg).
				File(llb.Mkfile("/etc/yum.repos.d/local-"+suffix+".repo", 0o644, localRepo), pg).
				Run(
					llb.AddMount("/tmp/st", rpms, llb.Readonly),
					dalec.ShArgsf("cp /tmp/st/RPMS/$(uname -m)/* %s/RPMS/ && cp /tmp/st/SRPMS/* %s/SRPMS", repoPath, repoPath),
					pg,
				).
				Run(dalec.ShArgs("createrepo --compatibility "+repoPath),
					pg,
				).Root()

			for _, opt := range opts {
				withRepos = withRepos.With(opt)
			}

			return withRepos
		}
	}
}
