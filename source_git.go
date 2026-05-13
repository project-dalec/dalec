package dalec

import (
	stderrors "errors"
	"fmt"
	"io"
	"net/url"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/util/gitutil"
)

type SourceGit struct {
	// URL is the URL of the git repository.
	URL string `yaml:"url" json:"url"`
	// Commit is the ref, which may be a commit, a tag, or even a full ref.
	// NOTE: When using a commit ref, tag info is *not* fetched.
	Commit string `yaml:"commit" json:"commit"`
	// Checksum is the expected commit hash for the resolved ref.
	// It is useful when "Commit" refers to a mutable ref, such as a tag.
	Checksum string `yaml:"checksum,omitempty" json:"checksum,omitempty"`
	// KeepGitDir includes the .git directory in the source.
	// NOTE: Not all git metadata may be available. The available metadata depends
	// on the ref specified by "Commit".
	KeepGitDir bool `yaml:"keepGitDir,omitempty" json:"keepGitDir,omitempty"`
	// Auth can be used to add instructions on how to authenticate with the git repository.
	Auth GitAuth `yaml:"auth,omitempty" json:"auth,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

type GitAuth struct {
	// Header is the name of the secret which contains the git auth header.
	// When using git auth header based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Header string `yaml:"header,omitempty" json:"header,omitempty"`
	// Token is the name of the secret which contains a git auth token when using
	// token based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
	// SSH is the name of the secret which contains the ssh auth info when using
	// ssh based auth.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	SSH string `yaml:"ssh,omitempty" json:"ssh,omitempty"`
}

type GomodGitAuth struct {
	// Header is the name of the secret that contains the git auth header.
	// when using git auth header based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Header string `yaml:"header,omitempty" json:"header,omitempty"`
	// Token is the name of the secret which contains a git auth token when using
	// token based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
	// SSH is a struct container the name of the ssh ID which contains the
	// address of the ssh auth socket, plus the username to use for the git
	// remote.
	// Note: This should not have the *actual* socket address, just the name of
	// the ssh ID which was specified as a build secret.
	SSH *GomodGitAuthSSH `yaml:"ssh,omitempty" json:"ssh,omitempty"`
}

type GomodGitAuthSSH struct {
	// ID is the name of the ssh socket to mount, as provided via the `--ssh`
	// flag to `docker build`.
	ID string `yaml:"id,omitempty" json:"id,omitempty"`
	// Username is the username to use with this particular git remote. If none
	// is provided, `git` will be inserted.
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
}

// SetGitOption returns an [llb.GitOption] which sets the auth header and token secret
// values in LLB if they are set.
func (a *GitAuth) SetGitOption(gi *llb.GitInfo) {
	if a == nil {
		return
	}
	if a.Header != "" {
		gi.AuthHeaderSecret = a.Header
	}

	if a.Token != "" {
		gi.AuthTokenSecret = a.Token
	}

	if a.SSH != "" {
		gi.MountSSHSock = a.SSH
	}
}

func (src *SourceGit) IsDir() bool {
	return true
}

func (src *SourceGit) validate(opts fetchOptions) error {
	var errs []error

	if src.URL == "" {
		errs = append(errs, fmt.Errorf("git source must have a URL"))
	}
	if src.Commit == "" {
		errs = append(errs, fmt.Errorf("git source must have a commit"))
	}

	if len(errs) > 0 {
		err := fmt.Errorf("invalid git source: %w", stderrors.Join(errs...))
		err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
		return err
	}

	return nil
}

func (src *SourceGit) toState(opts fetchOptions) llb.State {
	return src.baseState(opts).With(sourceFilters(opts))
}

func (src *SourceGit) baseState(opts fetchOptions) llb.State {
	var gOpts []llb.GitOption
	if src.KeepGitDir {
		gOpts = append(gOpts, llb.KeepGitDir())
	}
	if src.Checksum != "" {
		gOpts = append(gOpts, llb.GitChecksum(src.Checksum))
	}
	gOpts = append(gOpts, WithConstraints(opts.Constraints...))
	gOpts = append(gOpts, &src.Auth)
	gOpts = append(gOpts, src._sourceMap.GetRootLocation())

	return llb.Git(src.URL, src.Commit, gOpts...)
}

func (src *SourceGit) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	return src.baseState(opts).With(mountFilters(opts)), nil
}

func (git *SourceGit) fillDefaults(ls []*SourceGenerator) {
	if git == nil {
		return
	}

	host := git.URL

	u, err := url.Parse(git.URL)
	if err == nil {
		host = u.Host
	}

	// The git auth from the git source is autofilled for the gomods, so
	// the user doesn't have to repeat themselves.
	for _, gen := range ls {
		gen.fillDefaults(host, &git.Auth)
	}
}

func (src *SourceGit) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	var errs []error

	updated, err := expandArgs(lex, src.URL, args, allowArg)
	src.URL = updated
	if err != nil {
		errs = append(errs, err)
	}

	updated, err = expandArgs(lex, src.Commit, args, allowArg)
	src.Commit = updated
	if err != nil {
		errs = append(errs, err)
	}

	updated, err = expandArgs(lex, src.Checksum, args, allowArg)
	src.Checksum = updated
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		err := fmt.Errorf("failed to process build args for git source: %w", stderrors.Join(errs...))
		err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
		return err
	}
	return nil
}

func (src *SourceGit) doc(w io.Writer, name string) {
	u, err := url.Parse(src.URL)
	if err != nil {
		// This should have always been validated before, so we panic here
		panic(fmt.Errorf("could not parse git ref %q: %w", src.URL, err))
	}
	ref, err := gitutil.FromURL(u)
	if err != nil {
		// This should have always been validated before, so we panic here
		panic(fmt.Errorf("could not parse git ref %q: %w", src.URL, err))
	}

	printDocLn(w, "Generated from a git repository:")
	printDocLn(w, "	Remote:", ref.Remote)
	printDocLn(w, "	Ref:", src.Commit)
	if src.Checksum != "" {
		printDocLn(w, "	Checksum:", src.Checksum)
	}
}
