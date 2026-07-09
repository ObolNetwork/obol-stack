# Bake definition for stack-owned pure-Go images (Dockerfile.x402).
# Invoked by .github/workflows/docker-publish-x402.yml.
#
# Variables are set from the workflow environment:
#   SHA_SHORT, SHA_LONG, PUSH_LATEST ("true"|"false"), REGISTRY

variable "REGISTRY" {
  default = "ghcr.io/obolnetwork"
}

variable "SHA_SHORT" {
  default = "dev"
}

variable "SHA_LONG" {
  default = "dev"
}

variable "PUSH_LATEST" {
  default = "false"
}

function "tags" {
  params = [name]
  result = compact([
    "${REGISTRY}/${name}:${SHA_SHORT}",
    "${REGISTRY}/${name}:${SHA_LONG}",
    equal(PUSH_LATEST, "true") ? "${REGISTRY}/${name}:latest" : null,
  ])
}

group "default" {
  targets = [
    "x402-verifier",
    "x402-buyer",
    "serviceoffer-controller",
    "job-broker",
    "demo-server",
  ]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile.x402"
  platforms  = ["linux/amd64", "linux/arm64"]
  // One shared GHA cache for the whole group — builder stage is shared.
  cache-from = ["type=gha,scope=x402-go"]
  cache-to   = ["type=gha,scope=x402-go,mode=max"]
}

target "x402-verifier" {
  inherits = ["_common"]
  target   = "x402-verifier"
  tags     = tags("x402-verifier")
  labels = {
    "org.opencontainers.image.title"       = "x402-verifier"
    "org.opencontainers.image.description" = "x402 payment verification sidecar for Obol Stack"
    "org.opencontainers.image.vendor"      = "Obol"
    "org.opencontainers.image.source"      = "https://github.com/ObolNetwork/obol-stack"
  }
}

target "x402-buyer" {
  inherits = ["_common"]
  target   = "x402-buyer"
  tags     = tags("x402-buyer")
  labels = {
    "org.opencontainers.image.title"       = "x402-buyer"
    "org.opencontainers.image.description" = "x402 buy-side payment sidecar for Obol Stack"
    "org.opencontainers.image.vendor"      = "Obol"
    "org.opencontainers.image.source"      = "https://github.com/ObolNetwork/obol-stack"
  }
}

target "serviceoffer-controller" {
  inherits = ["_common"]
  target   = "serviceoffer-controller"
  tags     = tags("serviceoffer-controller")
  labels = {
    "org.opencontainers.image.title"       = "serviceoffer-controller"
    "org.opencontainers.image.description" = "ServiceOffer reconciler for Obol Stack monetization"
    "org.opencontainers.image.vendor"      = "Obol"
    "org.opencontainers.image.source"      = "https://github.com/ObolNetwork/obol-stack"
  }
}

target "job-broker" {
  inherits = ["_common"]
  target   = "job-broker"
  tags     = tags("job-broker")
  labels = {
    "org.opencontainers.image.title"       = "job-broker"
    "org.opencontainers.image.description" = "Async job delivery broker for x402-paid ServiceOffers"
    "org.opencontainers.image.vendor"      = "Obol"
    "org.opencontainers.image.source"      = "https://github.com/ObolNetwork/obol-stack"
  }
}

target "demo-server" {
  inherits = ["_common"]
  target   = "demo-server"
  tags     = tags("demo-server")
  labels = {
    "org.opencontainers.image.title"       = "demo-server"
    "org.opencontainers.image.description" = "Demo HTTP services for Obol Stack sell demo"
    "org.opencontainers.image.vendor"      = "Obol"
    "org.opencontainers.image.source"      = "https://github.com/ObolNetwork/obol-stack"
  }
}
