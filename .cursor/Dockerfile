FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Stable system toolchain. The Go server needs a C compiler because
# github.com/mattn/go-sqlite3 requires CGO; the Zed client is built for
# wasm32-wasip2 with Rust; python3 backs the Zed task-adapter tests.
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
    MISE_DATA_DIR=/home/ubuntu/.local/share/mise \
    CGO_ENABLED=1 \
    PATH=/home/ubuntu/.local/share/mise/shims:/home/ubuntu/.local/bin:/home/ubuntu/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# mise is the repository's toolchain source of truth (Go, Node.js,
# golangci-lint, vsce). The pinned versions from mise.toml are installed later
# in the repo-aware install phase; here we only provide the mise binary and put
# its shim directory on PATH for every shell.
RUN curl -fsSL https://mise.run | sh

# rustup supplies the Rust toolchain for the Zed extension. Pin the same
# channel and target declared in editors/zed/rust-toolchain.toml so the WASM
# build is self-contained.
RUN curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain 1.98.0 \
    && rustup component add clippy rustfmt \
    && rustup target add wasm32-wasip2
