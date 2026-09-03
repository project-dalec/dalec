FROM --platform=${BUILDPLATFORM} golang:1.26.5@sha256:3aff6657219a4d9c14e27fb1d8976c49c29fddb70ba835014f477e1c70636647 AS go

# Used by CI to run buildkit
# We manage the verison here so that Dependabot keeps it up to date.
FROM moby/buildkit:v0.33.0@sha256:6c2fa84a6b61ccd72899dde4239f8d5717f05f9a8ca6f3cad185fb1a95a94de3 AS buildkit

FROM go  AS frontend-build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0
ARG TARGETARCH TARGETOS GOFLAGS=-trimpath
ARG DALEC_FRONTEND_COVERAGE=0
ARG EXTRA_BUILD_FLAGS=""
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOFLAGS=${GOFLAGS}
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    if [ "${DALEC_FRONTEND_COVERAGE}" = "1" ]; then \
	go build ${EXTRA_BUILD_FLAGS} -cover -covermode=atomic -coverpkg=./... -o /frontend ./cmd/frontend ; \
    else \
        go build ${EXTRA_BUILD_FLAGS} -o /frontend ./cmd/frontend ; \
    fi

FROM scratch AS frontend
COPY --from=frontend-build /frontend /frontend
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts"
ENTRYPOINT ["/frontend"]
