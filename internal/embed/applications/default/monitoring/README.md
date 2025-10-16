# Monitoring Stack

The obol-stack monitoring system provides comprehensive observability for all
cluster applications using Prometheus and Grafana.

## Overview

**Components:**

- **Prometheus** - Metrics collection and storage (7 days retention, 10GB
  storage)
- **Grafana** - Dashboard visualization and exploration
- **Alertmanager** - Alert routing (disabled for local dev)
- **Node Exporter** - System metrics
- **kube-state-metrics** - Kubernetes object metrics

**Access:**

- Grafana: http://grafana.localhost:8080 (anonymous admin access)
- Prometheus: http://prometheus.localhost:8080

**Default Dashboards:**

- Kubernetes Cluster Overview
- Kubernetes Pods
- Node Exporter (system metrics)

## Architecture: Generic Discovery-Based Monitoring

The monitoring stack is designed to be **completely generic** and
**application-agnostic**. Applications integrate with monitoring through
Kubernetes-native discovery mechanisms without requiring any modifications to
the monitoring stack itself.

_NOTE_: TBD

### Key Design Principles

1. **Applications are self-contained** - Each application brings its own
   dashboards and ServiceMonitors
2. **Monitoring discovers everything automatically** - No central configuration
   required
3. **Label-based discovery** - Standard Kubernetes pattern for service discovery
4. **Separation of concerns** - Monitoring knows nothing about specific
   applications

### Discovery Mechanisms

```
┌─────────────────────────────────────────────────────────┐
│              Application (any namespace)                │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  1. ServiceMonitors with label:                         │
│     release: monitoring                                 │
│     → Prometheus discovers and scrapes                  │
│                                                         │
│  2. ConfigMaps with label:                              │
│     grafana_dashboard: "1"                              │
│     → Grafana sidecar discovers and loads               │
│                                                         │
└─────────────────────────────────────────────────────────┘
                           │
                           │ Auto-discovery
                           ▼
┌─────────────────────────────────────────────────────────┐
│           Monitoring Stack (monitoring namespace)       │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Prometheus Operator:                                   │
│    - serviceMonitorSelectorNilUsesHelmValues: false     │
│    - serviceMonitorNamespaceSelector: {}                │
│    → Discovers ServiceMonitors in ALL namespaces        │
│                                                         │
│  Grafana Sidecar:                                       │
│    - searchNamespace: ALL                               │
│    - label: grafana_dashboard                           │
│    - labelValue: "1"                                    │
│    → Discovers dashboard ConfigMaps in ALL namespaces   │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## Application Integration Guide

**Note:** Application integration documentation is incomplete and will be added
in a future update.

## Configuration

The monitoring stack is configured in `monitoring-stack.yaml` with the following
key settings:

### Prometheus

```yaml
prometheus:
  prometheusSpec:
    retention: 7d
    retentionSize: "5GB"
    storageSpec:
      volumeClaimTemplate:
        spec:
          resources:
            requests:
              storage: 10Gi

    # Enable cross-namespace discovery
    serviceMonitorSelectorNilUsesHelmValues: false
    serviceMonitorNamespaceSelector: {}
    podMonitorNamespaceSelector: {}
```

### Grafana

```yaml
grafana:
  adminPassword: admin
  grafana.ini:
    auth.anonymous:
      enabled: true
      org_role: Admin

  # Dashboard auto-discovery
  sidecar:
    dashboards:
      enabled: true
      label: grafana_dashboard
      labelValue: "1"
      searchNamespace: ALL  # Critical for cross-namespace discovery
      folderAnnotation: grafana_folder
```

## Resources

- [Prometheus Operator Documentation](https://prometheus-operator.dev/)
- [ServiceMonitor Specification](https://prometheus-operator.dev/docs/operator/design/#servicemonitor)
- [Grafana Sidecar Documentation](https://github.com/grafana/helm-charts/tree/main/charts/grafana#sidecar-for-dashboards)
- [kube-prometheus-stack Chart](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack)
