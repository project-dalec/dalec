# Dalec - Package and Container Builder

Dalec is a Docker BuildKit frontend for building system packages (RPM, DEB) and containers from declarative YAML specifications. It supports multiple Linux distributions and Windows containers.

Always reference these instructions first and fallback to search or bash commands only when you encounter unexpected information that does not match the info here.

## Working Effectively

### Bootstrap and Basic Development
- Download Go dependencies: `go mod download` -- takes ~5 seconds
- Run unit tests: `go test --test.short --timeout=10m ./...` -- takes ~52 seconds. NEVER CANCEL. Set timeout to 15+ minutes.
- Generate source files: `go generate` -- takes ~1 second
- Run custom linters: `go run ./cmd/lint ./...` -- takes ~3 seconds
- Validate generated files are up to date: `git diff --exit-code` after running `go generate`

### Building CLI Tools
- Build frontend binary: `go build -o /tmp/frontend ./cmd/frontend` -- takes ~1 second
- Build dalec-redirectio: `go build -o /tmp/dalec-redirectio ./cmd/dalec-redirectio` -- takes ~1 second
- Generate JSON schema: `go run ./cmd/gen-jsonschema` -- takes ~0.2 seconds
- All other CLI tools in `cmd/` can be built with `go build` or run with `go run`

### Docker Frontend Build System
**IMPORTANT**: Docker builds may fail in some environments due to TLS certificate issues with proxy.golang.org. This is an environmental limitation, not a code issue.

- Build frontend image: `docker buildx bake frontend` -- takes ~2-5 minutes when working. NEVER CANCEL. Set timeout to 15+ minutes.
- **Alternative when Docker builds fail**: Use host-compiled binaries for development and testing
- The frontend requires Docker BuildKit with buildx support

### Integration Testing
- Run specific distro tests: `go test -timeout=59m -v ./test -run=TestMariner2` -- takes 30+ minutes. NEVER CANCEL. Set timeout to 75+ minutes.
- Run all integration tests: `go test -timeout=59m -v ./test` -- takes 45+ minutes. NEVER CANCEL. Set timeout to 75+ minutes.
- Tests require Docker and BuildKit to be working properly
- Tests cover multiple Linux distributions: Mariner2, Azlinux3, Jammy, Noble, Bullseye, Bookworm, etc.

## Validation

### Always Run Before Committing
- `go test --test.short ./...` -- validates unit tests pass
- `go run ./cmd/lint ./...` -- validates custom linting rules
- `go generate && git diff --exit-code` -- validates generated files are up to date
- Consider running integration tests for your target distribution if making significant changes

### Manual Validation Scenarios
- **CLI Tool Testing**: Build and run `go build -o /tmp/frontend ./cmd/frontend` then test with `--help` flag
- **Schema Generation**: Run `go run ./cmd/gen-jsonschema` and verify JSON schema output is valid
- **Example Spec Validation**: Use `docs/examples/go-md2man.yml` as a test case for spec validation

### Docker Build Validation (When Working)
- Build example: `docker build -t go-md2man:test -f docs/examples/go-md2man.yml --target=azlinux3/rpm --output=_output .`
- Container example: `docker build -t go-md2man:test -f docs/examples/go-md2man.yml --target=azlinux3 .`

## Common Tasks

### Repo Structure
```
.
├── cmd/                    # CLI tools and binaries
│   ├── frontend/          # Main BuildKit frontend
│   ├── gen-jsonschema/    # JSON schema generator
│   ├── lint/              # Custom linters
│   └── ...                # Other tools
├── docs/examples/         # Example Dalec specs
├── test/                  # Integration tests
├── targets/               # Target-specific implementations
├── website/               # Documentation (Docusaurus)
├── docker-bake.hcl        # Docker Buildx configuration
├── Dockerfile             # Frontend container definition
└── go.mod                 # Go module definition
```

### Key Files to Monitor
- Always regenerate files after modifying source variants: `go generate ./...`
- Always run linting after changes: `go run ./cmd/lint ./...`
- Check `docker-bake.hcl` for Docker build targets and configurations
- Check `docs/examples/go-md2man.yml` for canonical example of Dalec spec format

### Important Directories
- `cmd/` - All CLI tools and main binaries
- `test/` - Comprehensive integration test suite
- `targets/` - Platform-specific build logic (Linux RPM/DEB, Windows)
- `frontend/` - Core BuildKit frontend implementation
- `website/docs/` - User documentation and examples

## Development Environment Requirements

### Required Tools
- Go 1.25+ (check with `go version`)
- Docker with BuildKit support (check with `docker buildx version`)
- Standard Unix tools (git, make, etc.)

### Optional but Recommended
- Node.js 18+ for documentation site (`cd website && npm start`)
- golangci-lint for additional linting (custom linter is used in addition to golangci-lint)

### Environment Limitations
- Docker builds require internet access
- Integration tests require full Docker functionality
- Host-based Go builds always work and are sufficient for most development

### Time Expectations
- Unit tests: ~1 minute
- Custom linting: ~3 seconds
- Code generation: ~1 second
- Frontend binary build: ~1 second
- Docker frontend build: 2-5 minutes (when working)
- Integration test suite: 45+ minutes for full suite, 30+ minutes for single distro

Remember: NEVER CANCEL long-running commands. Build and test operations can legitimately take 30-60+ minutes.
