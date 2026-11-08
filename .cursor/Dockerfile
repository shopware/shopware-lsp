FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Stable system toolchain. The Rust server bundles SQLite and the Go oracle
# needs CGO for github.com/mattn/go-sqlite3, so both need a C compiler; the
# Zed client is built for wasm32-wasip2; python3 backs the Zed task-adapter
# tests and scripts/bench.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        git \
        pkg-config \
        python3 \
        unzip \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Run as the non-root user that ships with the Ubuntu base image.
USER ubuntu
WORKDIR /home/ubuntu

ENV HOME=/home/ubuntu \
    CARGO_HOME=/home/ubuntu/.cargo \
    RUSTUP_HOME=/home/ubuntu/.rustup \
    CGO_ENABLED=1 \
    PATH=/home/ubuntu/.local/go/bin:/home/ubuntu/go/bin:/home/ubuntu/.local/node/bin:/home/ubuntu/.local/bin:/home/ubuntu/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Toolchain pins mirror CI: Go from go.mod (the oracle), Node.js 24 for the
# VS Code extension, golangci-lint from .github/workflows/tests.yml.
ARG GO_VERSION=1.27.0
ARG NODE_VERSION=24.18.1
ARG GOLANGCI_LINT_VERSION=v2.13.0
RUN mkdir -p /home/ubuntu/.local \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -xz -C /home/ubuntu/.local \
    && mkdir -p /home/ubuntu/.local/node \
    && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
        | tar -xJ --strip-components=1 -C /home/ubuntu/.local/node \
    && curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
        | sh -s -- -b /home/ubuntu/go/bin "${GOLANGCI_LINT_VERSION}"

# rustup supplies Rust. The server toolchain comes from rust-toolchain.toml
# (installed on first cargo use); the Zed client pins its own channel and the
# wasm32-wasip2 target in editors/zed/rust-toolchain.toml.
RUN curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain 1.98.0 \
    && rustup component add clippy rustfmt \
    && rustup target add wasm32-wasip2
