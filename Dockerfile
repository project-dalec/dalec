FROM --platform=${BUILDPLATFORM} golang:1.26.4@sha256:792443b89f65105abba56b9bd5e97f680a80074ac62fc844a584212f8c8102c3 AS go

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
