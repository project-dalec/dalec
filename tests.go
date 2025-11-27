package dalec

import (
	"context"
	goerrors "errors"
	"fmt"
	"io/fs"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// TestSpec is used to execute tests against a container with the package installed in it.
type TestSpec struct {
	// Name is the name of the test
	// This will be used to output the test results
	Name string `yaml:"name" json:"name" jsonschema:"required"`

	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to test the package.
	Steps []TestStep `yaml:"steps,omitempty" json:"steps,omitempty"`

	// Files is the list of files to check after running the steps.
	Files map[string]FileCheckOutput `yaml:"files,omitempty" json:"files,omitempty"`

	// unexported pointer to parsed source map for this TestSpec
	_sourceMap *sourceMap `json:"-" yaml:"-"`
}

// GetSourceLocation returns an llb.ConstraintsOpt for the TestSpec
func (t *TestSpec) GetSourceLocation(state llb.State) llb.ConstraintsOpt {
	return t._sourceMap.GetLocation(state)
}

// TestStep is a wrapper for [BuildStep] to include checks on stdio streams
type TestStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Stdin is the input to provide to the command when running tests
	Stdin string `yaml:"stdin,omitempty" json:"stdin,omitempty"`
	// Stdout describes checks to perform against stdout
	Stdout CheckOutput `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	// Stderr describes checks to perform against stderr
	Stderr CheckOutput `yaml:"stderr,omitempty" json:"stderr,omitempty"`

	// unexported pointer to parsed source map for this TestStep
	_commandSourceMap *sourceMap `json:"-" yaml:"-"`
}

func (step *TestStep) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal struct {
		Command sourceMappedValue[string] `yaml:"command" json:"command"`
		Env     map[string]string         `yaml:"env,omitempty" json:"env,omitempty"`
		Stdin   string                    `yaml:"stdin,omitempty" json:"stdin,omitempty"`
		Stdout  CheckOutput               `yaml:"stdout,omitempty" json:"stdout,omitempty"`
		Stderr  CheckOutput               `yaml:"stderr,omitempty" json:"stderr,omitempty"`
	}

	var i internal
	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode test step")
	}

	*step = TestStep{
		Command: i.Command.Value,
		Env:     i.Env,
		Stdin:   i.Stdin,
		Stdout:  i.Stdout,
		Stderr:  i.Stderr,
	}
	step._commandSourceMap = i.Command.sourceMap
	return nil
}

// GetSourceLocation returns the source mapping for the TestStep's command
func (ts *TestStep) GetSourceLocation(state llb.State) llb.ConstraintsOpt {
	return ts._commandSourceMap.GetLocation(state)
}

// CheckOutput is used to specify the expected output of a check, such as stdout/stderr or a file.
// All non-empty fields will be checked.
type CheckOutput struct {
	// Equals is the exact string to compare the output to.
	Equals string `yaml:"equals,omitempty" json:"equals,omitempty"`
	// Contains is the list of strings to check if they are contained in the output.
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"`
	// Matches is the list of regular expressions to match the output against.
	Matches []string `yaml:"matches,omitempty" json:"matches,omitempty"`
	// StartsWith is the string to check if the output starts with.
	StartsWith string `yaml:"starts_with,omitempty" json:"starts_with,omitempty"`
	// EndsWith is the string to check if the output ends with.
	EndsWith string `yaml:"ends_with,omitempty" json:"ends_with,omitempty"`
	// Empty is used to check if the output is empty.
	Empty bool `yaml:"empty,omitempty" json:"empty,omitempty"`

	equalsSourceMap     *sourceMap   `json:"-" yaml:"-"`
	startsWithSourceMap *sourceMap   `json:"-" yaml:"-"`
	endsWithSourceMap   *sourceMap   `json:"-" yaml:"-"`
	emptySourceMap      *sourceMap   `json:"-" yaml:"-"`
	containsSourceMaps  []*sourceMap `json:"-" yaml:"-"`
	matchesSourceMaps   []*sourceMap `json:"-" yaml:"-"`
}

// FileCheckOutput is used to specify the expected output of a file.
type FileCheckOutput struct {
	CheckOutput `yaml:",inline"`
	// Permissions is the expected permissions of the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// IsDir is used to set the expected file mode to a directory.
	IsDir bool `yaml:"is_dir,omitempty" json:"is_dir,omitempty"`
	// NotExist is used to check that the file does not exist.
	NotExist bool `yaml:"not_exist,omitempty" json:"not_exist,omitempty"`
	// field-level source maps for file-specific attributes
	permissionsSourceMap *sourceMap `json:"-" yaml:"-"`
	isDirSourceMap       *sourceMap `json:"-" yaml:"-"`
	notExistSourceMap    *sourceMap `json:"-" yaml:"-"`

	// TODO: Support checking symlinks
	// This is not currently possible with buildkit as it does not expose information about the symlink
}

func (check *FileCheckOutput) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	if node.Type() == ast.NullType {
		*check = FileCheckOutput{}
		return nil
	}

	// Custom unmarshallers with inline structs behave strangely (like fields not getting set properly, even on the main type).
	// For now split it out manually.
	type internal struct {
		Permissions sourceMappedValue[fs.FileMode] `yaml:"permissions,omitempty" json:"permissions"`
		IsDir       sourceMappedValue[bool]        `yaml:"is_dir,omitempty" json:"is_dir"`
		NotExist    sourceMappedValue[bool]        `yaml:"not_exist,omitempty" json:"not_exist"`

		Equals     ast.Node `yaml:"equals,omitempty" json:"equals"`
		Contains   ast.Node `yaml:"contains,omitempty" json:"contains"`
		Matches    ast.Node `yaml:"matches,omitempty" json:"matches"`
		StartsWith ast.Node `yaml:"starts_with,omitempty" json:"starts_with"`
		EndsWith   ast.Node `yaml:"ends_with,omitempty" json:"ends_with"`
		Empty      ast.Node `yaml:"empty,omitempty" json:"empty"`
	}

	dec := getDecoder(ctx)
	var i internal

	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "error unmarshalling file check output")
	}

	// Now for a 2nd pass, remove the fields we have already processed
	// and pass the rest to the CheckOutput unmarshaller
	// This is a bit hacky but it works around the limitations of the inline yaml.
	// It also makes sure this works with strict mode enabled.
	var values []*ast.MappingValueNode
	for _, v := range node.(*ast.MappingNode).Values {
		switch key := v.Key.(*ast.StringNode); key.Value {
		case "permissions", "is_dir", "not_exist":
		default:
			values = append(values, v)
		}
	}

	updated := ast.Mapping(node.GetToken(), false, values...)

	var i2 CheckOutput
	if err := dec.DecodeFromNodeContext(ctx, updated, &i2); err != nil {
		return err
	}

	*check = FileCheckOutput{
		Permissions: i.Permissions.Value,
		IsDir:       i.IsDir.Value,
		NotExist:    i.NotExist.Value,
		CheckOutput: i2,

		permissionsSourceMap: i.Permissions.sourceMap,
		isDirSourceMap:       i.IsDir.sourceMap,
		notExistSourceMap:    i.NotExist.sourceMap,
	}

	// a file check that does not set NotExist (explicitly) will not have a source map set
	// In this case the source map should point to the entire file check node
	if check.notExistSourceMap == nil {
		check.notExistSourceMap = newSourceMap(ctx, node)
	}

	return nil
}

func (check *CheckOutput) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal struct {
		Equals     sourceMappedValue[string]   `yaml:"equals,omitempty" json:"equals"`
		Contains   []sourceMappedValue[string] `yaml:"contains,omitempty" json:"contains"`
		Matches    []sourceMappedValue[string] `yaml:"matches,omitempty" json:"matches"`
		StartsWith sourceMappedValue[string]   `yaml:"starts_with,omitempty" json:"starts_with"`
		EndsWith   sourceMappedValue[string]   `yaml:"ends_with,omitempty" json:"ends_with"`
		Empty      sourceMappedValue[bool]     `yaml:"empty,omitempty" json:"empty"`
	}

	var i internal
	dec := getDecoder(ctx)
	err := dec.DecodeFromNodeContext(ctx, node, &i)
	if err != nil {
		return errors.Wrap(err, "error unmarshalling check output")
	}

	*check = CheckOutput{
		Equals:     i.Equals.Value,
		Contains:   make([]string, len(i.Contains)),
		Matches:    make([]string, len(i.Matches)),
		StartsWith: i.StartsWith.Value,
		EndsWith:   i.EndsWith.Value,
		Empty:      i.Empty.Value,

		equalsSourceMap:     i.Equals.sourceMap,
		startsWithSourceMap: i.StartsWith.sourceMap,
		endsWithSourceMap:   i.EndsWith.sourceMap,
		emptySourceMap:      i.Empty.sourceMap,
		containsSourceMaps:  make([]*sourceMap, len(i.Contains)),
		matchesSourceMaps:   make([]*sourceMap, len(i.Matches)),
	}

	for i, v := range i.Contains {
		check.Contains[i] = v.Value
		check.containsSourceMaps[i] = v.sourceMap
	}

	for i, v := range i.Matches {
		check.Matches[i] = v.Value
		check.matchesSourceMaps[i] = v.sourceMap
	}

	return nil
}

// CheckOutputError is used to build an error message for a failed output check for a test case.
type CheckOutputError struct {
	Kind     string
	Expected string
	Actual   string
	Path     string
	Index    *int
}

func (c *CheckOutputError) Error() string {
	return fmt.Sprintf("expected %q %s %q, got %q", c.Path, c.Kind, c.Expected, c.Actual)
}

// IsEmpty is used to determine if there are any checks to perform.
func (c CheckOutput) IsEmpty() bool {
	return c.Equals == "" && len(c.Contains) == 0 && len(c.Matches) == 0 && c.StartsWith == "" && c.EndsWith == "" && !c.Empty
}

func (t *TestSpec) validate() error {
	var errs []error

	for _, m := range t.Mounts {
		if err := m.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "mount %s", m.Dest))
		}
	}
	return goerrors.Join(errs...)
}

func (c *CheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	for i, contains := range c.Contains {
		updated, err := expandArgs(lex, contains, args, allowArg)
		if err != nil {
			return fmt.Errorf("%w: contains at list index %d", err, i)
		}
		c.Contains[i] = updated
	}

	updated, err := expandArgs(lex, c.EndsWith, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: endsWith", err)
	}
	c.EndsWith = updated

	for i, matches := range c.Matches {
		updated, err = expandArgs(lex, matches, args, allowArg)
		if err != nil {
			return fmt.Errorf("%w: matches at list index %d", err, i)
		}
		c.Matches[i] = updated
	}

	updated, err = expandArgs(lex, c.Equals, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: equals", err)
	}
	c.Equals = updated

	updated, err = expandArgs(lex, c.StartsWith, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: startsWith", err)
	}
	c.StartsWith = updated
	return nil
}

func (s *TestStep) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	for k, v := range s.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			appendErr(errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		s.Env[k] = updated
	}

	updated, err := expandArgs(lex, s.Stdin, args, allowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "stdin"))
	}
	if updated != s.Stdin {
		s.Stdin = updated
	}

	stdout := s.Stdout
	if err := stdout.processBuildArgs(lex, args, allowArg); err != nil {
		appendErr(errors.Wrap(err, "stdout"))
	}
	s.Stdout = stdout

	stderr := s.Stderr
	if err := stderr.processBuildArgs(lex, args, allowArg); err != nil {
		appendErr(errors.Wrap(err, "stderr"))
	}
	s.Stderr = stderr

	return goerrors.Join(errs...)
}

func (c *TestSpec) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	for i, s := range c.Mounts {
		if err := s.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(err)
			continue
		}
		c.Mounts[i] = s
	}

	for k, v := range c.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			appendErr(errors.Wrapf(err, "%s=%s", k, v))
			continue
		}
		c.Env[k] = updated
	}

	for i, step := range c.Steps {
		if err := step.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))
			continue
		}
		c.Steps[i] = step
	}

	for name, f := range c.Files {
		if err := f.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(fmt.Errorf("error performing shell expansion to check output of file %s: %w", name, err))
		}
		c.Files[name] = f
	}

	return errors.Wrap(goerrors.Join(errs...), c.Name)
}

func (c *FileCheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	check := c.CheckOutput
	if err := check.processBuildArgs(lex, args, allowArg); err != nil {
		return err
	}
	c.CheckOutput = check
	return nil
}

// GetSourceLocation returns the source maps for the check kind + index
func (c FileCheckOutput) GetSourceLocation(state llb.State, kind string, index int) llb.ConstraintsOpt {
	switch kind {
	case CheckFilePermissionsKind:
		return c.permissionsSourceMap.GetLocation(state)
	case CheckFileIsDirKind:
		return c.isDirSourceMap.GetLocation(state)
	case CheckFileNotExistsKind:
		return c.notExistSourceMap.GetLocation(state)
	default:
		// Delegate to embedded CheckOutput (equals/contains/...)
		return c.CheckOutput.GetSourceLocation(state, kind, index)
	}
}

func (c CheckOutput) GetSourceLocation(state llb.State, kind string, index int) llb.ConstraintsOpt {
	switch kind {
	case CheckOutputContainsKind:
		// locate matching contains entry
		return c.containsSourceMaps[index].GetLocation(state)
	case CheckOutputMatchesKind:
		return c.matchesSourceMaps[index].GetLocation(state)
	case CheckOutputStartsWithKind:
		return c.startsWithSourceMap.GetLocation(state)
	case CheckOutputEndsWithKind:
		return c.endsWithSourceMap.GetLocation(state)
	case CheckOutputEmptyKind:
		return c.emptySourceMap.GetLocation(state)
	case CheckOutputEqualsKind:
		return c.equalsSourceMap.GetLocation(state)
	}

	return nil
}

const (
	CheckFileNotExistsKind    = "not_exist"
	CheckFilePermissionsKind  = "permissions"
	CheckFileIsDirKind        = "is_dir"
	CheckOutputEmptyKind      = "empty"
	CheckOutputEqualsKind     = "equals"
	CheckOutputContainsKind   = "contains"
	CheckOutputMatchesKind    = "matches"
	CheckOutputStartsWithKind = "starts_with"
	CheckOutputEndsWithKind   = "ends_with"
)
