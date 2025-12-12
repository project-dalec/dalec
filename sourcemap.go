package dalec

import (
	"context"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
)

// Unexported source map stored on Spec
type sourceMap struct {
	filename string
	language string
	data     []byte
	pos      *pb.Range
}

type sourceMapContext struct {
	fileName string
	data     []byte
	language string
}

type sourceMapContextKey struct{}

func sourceMapInfo(ctx context.Context) sourceMapContext {
	v := ctx.Value(sourceMapContextKey{})
	if v == nil {
		return sourceMapContext{}
	}
	return v.(sourceMapContext)
}

// LocationConstraint returns an llb.ConstraintsOpt for the given yamlPath, or nil when not present
func (sm *sourceMap) GetLocation(state llb.State) (ret llb.ConstraintsOpt) {
	return sm.getLocation(&state)
}

func (sm *sourceMap) GetRootLocation() llb.ConstraintsOpt {
	return sm.getLocation(nil)
}

func (sm *sourceMap) getLocation(st *llb.State) llb.ConstraintsOpt {
	if sm == nil {
		return ConstraintsOptFunc(func(*llb.Constraints) {})
	}
	sourceMap := llb.NewSourceMap(st, sm.filename, sm.language, sm.data)
	return sourceMap.Location([]*pb.Range{sm.pos})
}

func (sm *sourceMap) GetErrdefsSource() *errdefs.Source {
	if sm == nil {
		return nil
	}
	return &errdefs.Source{
		Info: &pb.SourceInfo{
			Filename: sm.filename,
			Data:     sm.data,
			Language: sm.language,
		},
		Ranges: []*pb.Range{sm.pos},
	}
}

// nodeToRange converts an AST node to a protobuf Range
func nodeToRange(node ast.Node) *pb.Range {
	token := node.GetToken()

	pos := token.Position
	start := &pb.Position{
		Line:      int32(pos.Line),
		Character: int32(pos.Column),
	}

	walk := &endPosVisitor{}
	ast.Walk(walk, node)

	return &pb.Range{
		Start: start,
		End:   walk.Position(),
	}
}

type endPosVisitor struct {
	endLine int
	endChar int
}

func (v *endPosVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}

	if n.Type() == ast.CommentType {
		return v
	}

	pos := n.GetToken().Position
	if pos.Line < v.endLine {
		return v
	}

	v.endLine = pos.Line
	v.endChar = pos.Column

	if n.Type() != ast.StringType {
		return v
	}

	setEndChar := func(ns string) {
		newlines := strings.Count(ns, "\n")
		if newlines == 0 {
			v.endChar = pos.Column + len(ns)
			return
		}

		// If the string ends with a newline, we want to end at the last character
		// of the last line with content, not at character 1 of the next line
		if strings.HasSuffix(ns, "\n") {
			ns = strings.TrimSuffix(ns, "\n")
			newlines--
		}

		v.endLine += newlines
		last := strings.LastIndex(ns, "\n")
		if last == -1 {
			// Single line (or what's left after trimming trailing newline)
			v.endChar = pos.Column + len(ns)
		} else {
			// Multiple lines - end character is the length of the last line
			v.endChar = len(ns) - last
		}
	}

	// Work around panic calling `n.String()` on *ast.StringNode
	// Ref: https://github.com/goccy/go-yaml/issues/797
	// This only happens in some specific cases, not on every string node.
	// In this case use the `Value` field from the string node.
	// Its not as accurate since there's some extra formatting that `n.String()` does here,
	// but at least in terms of getting the right line number this will be decent.
	defer func() {
		recover() //nolint:errcheck
		setEndChar(n.(*ast.StringNode).Value)
	}()
	setEndChar(n.String())

	return v
}

func (v *endPosVisitor) Position() *pb.Position {
	return &pb.Position{
		Line:      int32(v.endLine),
		Character: int32(v.endChar),
	}
}

func newSourceMap(ctx context.Context, node ast.Node) *sourceMap {
	smCtx := sourceMapInfo(ctx)
	return &sourceMap{
		filename: smCtx.fileName,
		language: smCtx.language,
		data:     smCtx.data,
		pos:      nodeToRange(node),
	}
}

// MergeSourceLocations multiple source locations into one location for each file found.
// It only merges locations for the provided opts, not all locations in the constraints.
// This is useful when there multiple ranges in a file for a single operation.
func MergeSourceLocations(opts ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return ConstraintsOptFunc(func(c *llb.Constraints) {
		if len(opts) == 0 {
			return
		}

		var c2 llb.Constraints

		for _, opt := range opts {
			opt.SetConstraintsOption(&c2)
		}

		fileIdx := make(map[string]*llb.SourceLocation, len(c.SourceLocations))
		for _, loc := range c2.SourceLocations {
			v, ok := fileIdx[loc.SourceMap.Filename]
			if !ok {
				fileIdx[loc.SourceMap.Filename] = loc
				continue
			}
			v.Ranges = append(v.Ranges, loc.Ranges...)
		}

		for _, loc := range fileIdx {
			c.SourceLocations = append(c.SourceLocations, loc)
		}
	})
}

// sourceMappedValue is useful for unmarshalling core types (string, int, bool,
// etc) that you want to capture the source map for.
type sourceMappedValue[T any] struct {
	Value     T
	sourceMap *sourceMap
}

func (s *sourceMappedValue[T]) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal sourceMappedValue[T]
	var i internal

	if err := yaml.NodeToValue(node, &i.Value, decodeOpts(ctx)...); err != nil {
		return err
	}

	s.Value = i.Value
	s.sourceMap = newSourceMap(ctx, node)
	return nil
}
