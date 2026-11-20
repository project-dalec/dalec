FROM --platform=${BUILDPLATFORM} golang:1.27.3@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e AS go

# Used by CI to run buildkit
# We manage the verison here so that Dependabot keeps it up to date.
FROM moby/buildkit:v0.35.0@sha256:aa632b921ef07411d2cb77292cd34213d4f7b384001d6fceef1e1f25bf9752bb AS buildkit

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
