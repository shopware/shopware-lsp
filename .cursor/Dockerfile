FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Stable system toolchain. The Rust server bundles SQLite, so it needs a C
# compiler; the Zed client is built for wasm32-wasip2; python3 backs the Zed task-adapter
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
    PATH=/home/ubuntu/.local/node/bin:/home/ubuntu/.local/bin:/home/ubuntu/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Toolchain pins mirror CI: Node.js 24 for the VS Code extension.
ARG NODE_VERSION=24.18.1
RUN mkdir -p /home/ubuntu/.local/node \
    && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
        | tar -xJ --strip-components=1 -C /home/ubuntu/.local/node

# rustup supplies Rust. The server toolchain comes from rust-toolchain.toml
# (installed on first cargo use); the Zed client pins its own channel and the
# wasm32-wasip2 target in editors/zed/rust-toolchain.toml.
RUN curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain 1.98.0 \
    && rustup component add clippy rustfmt \
    && rustup target add wasm32-wasip2
