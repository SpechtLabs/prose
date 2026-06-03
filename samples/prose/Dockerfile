# Generic builder for the prose example operators.
#
# It must be built from the REPOSITORY ROOT, because each example's go.mod has
# `replace github.com/spechtlabs/prose => ../../..` — the framework source has to be
# inside the build context. The per-example operator-sdk Dockerfile (sample as
# context) can't see it and fails at `go mod download`.
#
#   docker build -f samples/prose/Dockerfile --build-arg EXAMPLE=wormhole-operator \
#     -t <registry>/<example>:<tag> .
#
# The builder stage pins to the build platform and cross-compiles via TARGETOS/
# TARGETARCH, so multi-arch images build natively (no QEMU) — the final stage only
# copies a static binary.
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
ARG EXAMPLE
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# The framework module (the replace target): module files plus every package it
# exports (pkg/prose facade over internal/pipeline + internal/observability).
COPY go.mod go.sum ./
COPY pkg/ pkg/
COPY internal/ internal/

# The selected example module.
COPY samples/prose/${EXAMPLE}/ samples/prose/${EXAMPLE}/

WORKDIR /workspace/samples/prose/${EXAMPLE}
RUN go mod download
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o /manager ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
