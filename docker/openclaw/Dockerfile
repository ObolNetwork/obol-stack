# Patch Dockerfile: layers Foundry CLI tools onto the upstream OpenClaw base image.
# Built by .github/workflows/docker-publish-openclaw.yml as the second stage
# after the base image is built from upstream source.
#
# Usage (CI):
#   docker build --build-arg BASE_TAG=v2026.2.22 --build-arg FOUNDRY_TAG=v1.5.1 \
#     -f docker/openclaw/Dockerfile .
#
ARG FOUNDRY_TAG
ARG BASE_TAG=latest

FROM ghcr.io/foundry-rs/foundry:${FOUNDRY_TAG} AS foundry

FROM ghcr.io/obolnetwork/openclaw-base:${BASE_TAG}

USER root

# Copy statically-linked Foundry binaries from the official image.
COPY --from=foundry /usr/local/bin/cast /usr/local/bin/cast
COPY --from=foundry /usr/local/bin/forge /usr/local/bin/forge
COPY --from=foundry /usr/local/bin/anvil /usr/local/bin/anvil

# Verify binaries run on the target architecture.
RUN cast --version && forge --version && anvil --version

# Restore non-root user from upstream image.
USER node
