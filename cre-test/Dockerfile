# syntax=docker/dockerfile:1
# ContribOracle runner: HTTP API in front of the CRE workflow simulator.
ARG GO_VERSION=1.25.3

# Build stage: runner binary + workflow WASM (compiled once, reused by every simulation).
FROM golang:${GO_VERSION}-trixie AS build
ARG CRE_CLI_VERSION=v1.37.0
ARG TARGETARCH
RUN curl -fsSL -o /tmp/cre.tar.gz \
      "https://github.com/smartcontractkit/cre-cli/releases/download/${CRE_CLI_VERSION}/cre_linux_${TARGETARCH}.tar.gz" \
 && tar -xzf /tmp/cre.tar.gz -C /tmp \
 && mv "/tmp/cre_${CRE_CLI_VERSION}_linux_${TARGETARCH}" /usr/local/bin/cre \
 && rm /tmp/cre.tar.gz \
 && cre version

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/runner ./cmd/runner \
 && cre workflow build test-workflow -o /out/workflow.wasm

# Runtime stage: no Go toolchain needed, simulations use the prebuilt WASM.
# trixie: the cre CLI binary needs glibc >= 2.38 (bookworm has 2.36).
FROM debian:trixie-slim
RUN useradd --create-home --uid 10001 app

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/local/bin/cre /usr/local/bin/cre
COPY --from=build /out/runner /usr/local/bin/runner

WORKDIR /app
# Owned by app: host files may be 0600 (e.g. from cre init) and must stay readable.
COPY --chown=app:app project.yaml secrets.yaml .env.public ./
COPY --chown=app:app test-workflow/workflow.yaml test-workflow/config.staging.json test-workflow/config.production.json test-workflow/
COPY --chown=app:app --from=build /out/workflow.wasm build/workflow.wasm

USER app
ENV PORT=8080 \
    CRE_PROJECT_DIR=/app \
    CRE_WORKFLOW=test-workflow \
    CRE_WASM=/app/build/workflow.wasm \
    CRE_TARGET=staging-settings
EXPOSE 8080
ENTRYPOINT ["runner"]
