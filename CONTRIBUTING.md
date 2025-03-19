# Contributing to Blockchain Helm Charts

Thank you for considering contributing to this project! This document provides guidelines to help you contribute effectively.

## Getting Started

### Prerequisites

- Kubernetes knowledge
- Helm chart development experience
- Understanding of the specific blockchain client you're creating/modifying a chart for

### Development Environment

1. Install [Helm](https://helm.sh/docs/intro/install/)
2. Install [kubectl](https://kubernetes.io/docs/tasks/tools/)
3. Set up a Kubernetes environment (minikube, kind, or a cloud provider)

## Chart Development Guidelines

### Chart Structure

Each chart should follow this structure:
```
charts/<blockchain-client>/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   ├── secret.yaml (if needed)
│   ├── pvc.yaml (if needed)
│   └── NOTES.txt
├── OWNERS (maintainers list)
└── README.md (chart-specific documentation)
```

### Requirements

- Charts must be compatible with Helm 3
- Include comprehensive documentation
- Provide sensible defaults in values.yaml
- Include proper Kubernetes resource requests and limits
- Follow security best practices

### Values.yaml

- Group related values logically
- Add comments explaining the purpose of values
- Include sensible defaults that work out-of-the-box
- Provide examples for custom configurations

## Pull Request Process

1. Fork the repository
2. Create a new branch for your changes
3. Make your changes following the chart development guidelines
4. Test your charts thoroughly
5. Submit a pull request
6. Address review comments

### Pull Request Checklist

- [ ] Chart version updated according to semantic versioning
- [ ] Chart README.md updated with any new values or changes
- [ ] Chart has been tested and verified to work
- [ ] `helm lint` passes without warnings
- [ ] `helm template` generates valid Kubernetes resources

## Testing Your Chart

```bash
# Lint the chart
helm lint charts/your-chart

# Render the templates
helm template charts/your-chart

# Install the chart in a test environment
helm install test-release charts/your-chart --dry-run
```

## Code of Conduct

Please respect other contributors and maintain a positive environment for everyone.

## Thank You

Your contributions help make this project better for everyone!
