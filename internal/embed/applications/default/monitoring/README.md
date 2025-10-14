# Monitoring Stack

The obol-stack monitoring system provides comprehensive observability for all cluster applications using Prometheus and Grafana.

## Overview

**Components:**
- **Prometheus** - Metrics collection and storage (7 days retention, 10GB storage)
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

The monitoring stack is designed to be **completely generic** and **application-agnostic**. Applications integrate with monitoring through Kubernetes-native discovery mechanisms without requiring any modifications to the monitoring stack itself.

### Key Design Principles

1. **Applications are self-contained** - Each application brings its own dashboards and ServiceMonitors
2. **Monitoring discovers everything automatically** - No central configuration required
3. **Label-based discovery** - Standard Kubernetes pattern for service discovery
4. **Separation of concerns** - Monitoring knows nothing about specific applications

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

This section describes how applications should integrate with the monitoring stack. **No modifications to this monitoring stack are required** - all integration is done from the application side.

### 1. Metrics Collection (Prometheus)

Applications expose metrics via **ServiceMonitors** that Prometheus automatically discovers.

#### Step 1: Expose Metrics Endpoint

Ensure your application exposes Prometheus metrics on an HTTP endpoint:

```yaml
# In your application deployment
spec:
  containers:
  - name: my-app
    ports:
    - name: metrics
      containerPort: 9090
      protocol: TCP
```

#### Step 2: Create a Service

Create a Kubernetes Service for the metrics endpoint:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-app
  namespace: my-app
  labels:
    app: my-app
spec:
  ports:
  - name: metrics
    port: 9090
    targetPort: metrics
  selector:
    app: my-app
```

#### Step 3: Create a ServiceMonitor

Create a ServiceMonitor resource with the **required label**:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-app
  namespace: my-app
  labels:
    release: monitoring  # ⚠️ REQUIRED - This label enables discovery
spec:
  selector:
    matchLabels:
      app: my-app
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

**Critical: The `release: monitoring` label is required for Prometheus to discover the ServiceMonitor.**

#### ServiceMonitor Configuration Options

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-app
  namespace: my-app
  labels:
    release: monitoring  # Required for discovery
spec:
  selector:
    matchLabels:
      app: my-app

  # Multiple endpoints (if your app exposes multiple metric ports)
  endpoints:
  - port: metrics
    interval: 30s           # Scrape frequency
    path: /metrics          # Metrics endpoint path
    scrapeTimeout: 10s      # Timeout for scrapes

  # Optional: Relabeling (modify labels before storing)
  - port: metrics
    interval: 30s
    relabelings:
    - sourceLabels: [__meta_kubernetes_pod_name]
      targetLabel: pod

  # Optional: Metric relabeling (modify metrics)
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: 'some_metric_.*'
      action: drop
```

#### Verification

After creating the ServiceMonitor:

1. **Check Prometheus targets**:
   ```bash
   kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
   # Open http://localhost:9090/targets
   # Look for your ServiceMonitor target
   ```

2. **Query metrics**:
   ```bash
   # In Prometheus UI, try querying your metrics
   # Example: up{job="my-app"}
   ```

3. **Debug ServiceMonitor discovery**:
   ```bash
   # Check if ServiceMonitor was created
   kubectl get servicemonitors -n my-app

   # Check labels
   kubectl get servicemonitor my-app -n my-app -o yaml | grep -A 5 labels
   ```

### 2. Dashboard Provisioning (Grafana)

Applications provision dashboards via **ConfigMaps** that Grafana's sidecar automatically discovers.

#### Pattern 1: Static ConfigMap (Simple Dashboards)

For simple, hand-crafted dashboards:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-app-dashboard
  namespace: my-app
  labels:
    grafana_dashboard: "1"  # ⚠️ REQUIRED - Enables discovery
  annotations:
    grafana_folder: "MyApp"  # Optional - Organizes into folder
data:
  my-dashboard.json: |
    {
      "dashboard": {
        "title": "My Application",
        "panels": [...],
        ...
      }
    }
```

**The dashboard appears in Grafana within ~30 seconds.**

#### Pattern 2: Dashboard Provisioner Job (Recommended)

For dashboards from Grafana.com or complex provisioning:

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: dashboard-provisioner
  namespace: my-app
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: dashboard-provisioner
  namespace: my-app
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["create", "update", "patch", "get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: dashboard-provisioner
  namespace: my-app
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: dashboard-provisioner
subjects:
- kind: ServiceAccount
  name: dashboard-provisioner
---
apiVersion: batch/v1
kind: Job
metadata:
  name: dashboard-provisioner
  namespace: my-app
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-weight: "5"
    helm.sh/hook-delete-policy: before-hook-creation
spec:
  ttlSecondsAfterFinished: 300
  template:
    spec:
      serviceAccountName: dashboard-provisioner
      restartPolicy: Never
      containers:
      - name: provisioner
        image: bitnami/kubectl:latest
        command:
        - /bin/bash
        - -c
        - |
          set -euo pipefail

          # Define dashboards (Grafana.com ID:Revision)
          declare -A DASHBOARDS=(
            ["my-dashboard"]="<grafana-id>:<revision>"
          )

          # Download and create ConfigMap
          for name in "${!DASHBOARDS[@]}"; do
            IFS=':' read -r id rev <<< "${DASHBOARDS[$name]}"

            # Download from Grafana.com
            curl -sSLf "https://grafana.com/api/dashboards/${id}/revisions/${rev}/download" \
              -o "/tmp/${name}.json"

            # Create ConfigMap with discovery labels
            kubectl create configmap "grafana-dashboard-${name}" \
              --from-file="${name}.json=/tmp/${name}.json" \
              --namespace=my-app \
              --dry-run=client -o yaml | \
            kubectl label --local -f - \
              grafana_dashboard=1 \
              --dry-run=client -o yaml | \
            kubectl annotate --local -f - \
              grafana_folder=MyApp \
              --dry-run=client -o yaml | \
            kubectl apply -f -

            echo "✓ Provisioned ${name} dashboard"
          done
```

**Benefits of the Job pattern:**
- Downloads dashboards at deploy time (not embedded in binary)
- Version pinning via revision numbers
- Automatic cleanup (TTL)
- Works with Helm hooks

#### Dashboard Folder Organization

Use the `grafana_folder` annotation to organize dashboards:

```yaml
metadata:
  annotations:
    grafana_folder: "Ethereum"     # Creates "Ethereum" folder in Grafana
    grafana_folder: "Monitoring"   # Creates "Monitoring" folder
```

Dashboards without this annotation appear in the root dashboard list.

#### Dashboard Verification

1. **Check ConfigMap creation**:
   ```bash
   kubectl get configmaps -n my-app -l grafana_dashboard=1
   ```

2. **Check Grafana sidecar logs**:
   ```bash
   kubectl logs -n monitoring -l app.kubernetes.io/name=grafana -c grafana-sc-dashboard
   # Look for: "Triggered watch for grafana-dashboard-my-app"
   ```

3. **Access Grafana**:
   ```bash
   # Open http://grafana.localhost:8080
   # Navigate to: Dashboards → Browse → MyApp folder
   ```

### 3. Complete Application Example

Here's a complete Helmfile-based application with full monitoring integration:

```yaml
# helmfile.yaml
repositories:
  - name: my-charts
    url: https://charts.example.com

releases:
  - name: my-app
    namespace: my-app
    createNamespace: true
    chart: my-charts/my-app
    version: 1.0.0
    values:
      - values.yaml
      - dashboards.yaml  # Dashboard provisioner
```

```yaml
# values.yaml
serviceMonitor:
  enabled: true
  labels:
    release: monitoring  # Critical for discovery
  interval: 30s
  path: /metrics

metrics:
  enabled: true
  port: 9090
```

```yaml
# dashboards.yaml (inline or separate file)
apiVersion: batch/v1
kind: Job
metadata:
  name: dashboard-provisioner
  annotations:
    helm.sh/hook: post-install,post-upgrade
# ... (see Pattern 2 above for full Job definition)
```

### 4. Troubleshooting Integration

#### Metrics Not Appearing in Prometheus

1. **Check ServiceMonitor exists**:
   ```bash
   kubectl get servicemonitor -n my-app
   ```

2. **Verify `release: monitoring` label**:
   ```bash
   kubectl get servicemonitor my-app -n my-app -o yaml | grep -A 3 labels
   ```

3. **Check Prometheus targets**:
   ```bash
   kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
   # Open http://localhost:9090/targets
   # Search for "my-app"
   ```

4. **Check Prometheus Operator logs**:
   ```bash
   kubectl logs -n monitoring -l app.kubernetes.io/name=prometheus-operator
   ```

5. **Verify metrics endpoint is accessible**:
   ```bash
   kubectl port-forward -n my-app svc/my-app 9090:9090
   curl http://localhost:9090/metrics
   ```

#### Dashboards Not Appearing in Grafana

1. **Check ConfigMap exists**:
   ```bash
   kubectl get configmap -n my-app -l grafana_dashboard=1
   ```

2. **Verify `grafana_dashboard: "1"` label**:
   ```bash
   kubectl get configmap my-dashboard -n my-app -o yaml | grep grafana_dashboard
   ```

3. **Check Grafana sidecar logs**:
   ```bash
   kubectl logs -n monitoring -l app.kubernetes.io/name=grafana -c grafana-sc-dashboard
   ```

4. **Force sidecar refresh** (if needed):
   ```bash
   kubectl delete pod -n monitoring -l app.kubernetes.io/name=grafana
   # Pod will restart and rediscover all dashboards
   ```

5. **Verify sidecar configuration**:
   ```bash
   kubectl get deployment -n monitoring kube-prometheus-stack-grafana -o yaml | grep -A 10 "searchNamespace"
   # Should show: searchNamespace: ALL
   ```

## Advanced Topics

### Custom Alert Rules

Applications can define PrometheusRule resources:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: my-app-alerts
  namespace: my-app
  labels:
    release: monitoring
spec:
  groups:
  - name: my-app
    interval: 30s
    rules:
    - alert: HighErrorRate
      expr: rate(my_app_errors_total[5m]) > 0.05
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "High error rate detected"
```

### PodMonitor (Alternative to ServiceMonitor)

For direct pod scraping without a Service:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: my-app
  namespace: my-app
  labels:
    release: monitoring
spec:
  selector:
    matchLabels:
      app: my-app
  podMetricsEndpoints:
  - port: metrics
    interval: 30s
```

### Multiple Grafana Datasources

Dashboards automatically use the "Prometheus" datasource. For custom datasources:

```json
{
  "dashboard": {
    "panels": [{
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      }
    }]
  }
}
```

## Reference Architecture

For a complete working example, see the **ethereum application**:
- `internal/embed/applications/ethereum/values.yaml` - ServiceMonitor configuration
- `internal/embed/applications/ethereum/dashboards.yaml` - Dashboard provisioner Job
- `internal/embed/applications/ethereum/DASHBOARDS.md` - Detailed documentation

## Configuration

The monitoring stack is configured in `monitoring-stack.yaml` with the following key settings:

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
