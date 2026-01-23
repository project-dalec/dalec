package gitservices

import (
	"path/filepath"

	"github.com/moby/buildkit/identity"
)

// Attributes are the basic pieces of information needed to host two git
// servers, one via SSH and one via HTTP
type Attributes struct {
	// ServerRoot is the root filesystem path of the git server. URL paths
	// refer to repositories relative to this root
	ServerRoot string
	// PrivateRepoPath is the filesystem path, relative to `serverRoot`, where
	// the private git repository is hosted
	PrivateRepoPath string
	// privateRepoPath is the URI path in the name of the public go module;
	// this public go module has a private dependency on the repo at
	// `privateRepoPath`
	DependingRepoPath string
	// HTTP Server path is the filesystem path of the already-built HTTP
	// server, installed into its final location.
	HTTPServerPath string

	// PrivateGomoduleHost is the hostname of the git server
	PrivateGomoduleHost string

	// HTTPPort is the port on which the http git server runs
	HTTPPort string
	// SSHPort is the port on which the ssh git server runs
	SSHPort string

	// HTTPServerBuildDir is the location at which the HTTP server program's
	// code will be loaded for building
	HTTPServerBuildDir string
	// HTTPServerBuildPath is the *local filesystem* location at which the HTTP server program's
	// code can be found
	HTTPServeCodeLocalPath string
	// OutDir is the location to which dalec will output files
	OutDir string

	// ModFileGoVersion is the go version for the go modules
	ModFileGoVersion string

	// tag is a private field and should not be accessed directly
	tag string
}

func (a *Attributes) PrivateGoModuleGitTag() string {
	if a.tag == "" {
		panic("PrivateGoModuleGitTag() called with empty tag")
	}
	return a.tag
}

func (a *Attributes) WithNewPrivateGoModuleGitTag() *Attributes {
	if a == nil {
		return &Attributes{tag: identity.NewID()}
	}

	b := *a
	b.tag = identity.NewID()

	return &b
}

func (a *Attributes) RepoAbsDir() string {
	return filepath.Join(a.ServerRoot, a.PrivateRepoPath)
}

// `PrivateRepoAbsPath` returns the resolved absolute filepath of the private
// git repo in the `gitHost` container.
func (g *Attributes) PrivateRepoAbsPath() string {
	return filepath.Join(g.ServerRoot, g.PrivateRepoPath)
}

func (g *Attributes) HTTPServerDir() string {
	return filepath.Dir(g.HTTPServerPath)
}

func (g *Attributes) HTTPServerBase() string {
	return filepath.Base(g.HTTPServerPath)
}
