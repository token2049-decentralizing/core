# syntax=docker/dockerfile:1
# cre-runner: GitHub webhooks + dashboard API + CRE workflow executions + LLM reviewer.
ARG GO_VERSION=1.26.2
# Matches GOTOOLCHAIN in cre/.env.public, so workflow builds are reproducible.
ARG WORKFLOW_GO_VERSION=1.25.3

# CRE CLI + workflow WASM (compiled once, reused by every execution).
FROM golang:${WORKFLOW_GO_VERSION}-trixie AS workflow
ARG CRE_CLI_VERSION=v1.37.0
ARG TARGETARCH
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}" \
 && curl -fsSL -o /tmp/cre.tar.gz \
      "https://github.com/smartcontractkit/cre-cli/releases/download/${CRE_CLI_VERSION}/cre_linux_${arch}.tar.gz" \
 && tar -xzf /tmp/cre.tar.gz -C /tmp \
 && mv "/tmp/cre_${CRE_CLI_VERSION}_linux_${arch}" /usr/local/bin/cre \
 && rm /tmp/cre.tar.gz \
 && cre version
WORKDIR /src
COPY cre/go.mod cre/go.sum ./
RUN go mod download
COPY cre/ ./
# The CLI does not create the output directory.
RUN mkdir -p /out && cre workflow build test-workflow -o /out/workflow.wasm

FROM golang:${GO_VERSION}-trixie AS build
WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -v -o /run-app .

# Runtime: no Go toolchain needed, executions use the prebuilt WASM.
# trixie: the cre CLI binary needs glibc >= 2.38 (bookworm has 2.36).
FROM debian:trixie-slim
# ~/.cre exists and is owned by app, so a volume mounted there (docker-compose.yml) is writable.
RUN useradd --create-home --uid 10001 app \
 && install -d -o app -g app -m 700 /home/app/.cre
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=workflow /usr/local/bin/cre /usr/local/bin/cre
COPY --from=build /run-app /usr/local/bin/

WORKDIR /app/cre
# Owned by app: host files may be 0600 (e.g. from cre init) and must stay readable.
COPY --chown=app:app cre/project.yaml cre/secrets.yaml cre/.env.public cre/simulation-limits.json ./
COPY --chown=app:app cre/test-workflow/workflow.yaml cre/test-workflow/config.local.json \
     cre/test-workflow/config.docker.json cre/test-workflow/config.production.json test-workflow/
COPY --chown=app:app --from=workflow /out/workflow.wasm build/workflow.wasm

USER app
WORKDIR /app
ENV PORT=8080 \
    CRE_PROJECT_DIR=/app/cre \
    CRE_WORKFLOW=test-workflow \
    CRE_WASM=/app/cre/build/workflow.wasm \
    CRE_TARGET=docker-simulation
# CRE_ETH_PRIVATE_KEY is not baked in: plain simulation uses the CLI's default key, and
# `--broadcast` (Solana payouts) refuses that default, so it must be a real key (Fly secret).
CMD ["run-app"]
