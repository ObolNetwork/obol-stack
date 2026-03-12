# k3s GPU Worker Deployment Example

This example shows the minimal shape for running the autoresearch worker inside a `k3s` cluster on a GPU host.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: autoresearch-worker
  namespace: autoresearch
spec:
  replicas: 1
  selector:
    matchLabels:
      app: autoresearch-worker
  template:
    metadata:
      labels:
        app: autoresearch-worker
    spec:
      containers:
        - name: worker
          image: autoresearch-worker:dev
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: DATA_DIR
              value: /data
            - name: AUTORESEARCH_REPO
              value: /data/autoresearch
            - name: EXPERIMENT_TIMEOUT_SECONDS
              value: "300"
          resources:
            limits:
              nvidia.com/gpu: 1
          volumeMounts:
            - name: worker-data
              mountPath: /data
      volumes:
        - name: worker-data
          persistentVolumeClaim:
            claimName: autoresearch-worker-data
---
apiVersion: v1
kind: Service
metadata:
  name: autoresearch-worker
  namespace: autoresearch
spec:
  selector:
    app: autoresearch-worker
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

After the Service exists, expose it with:

```bash
obol sell http autoresearch-worker \
  --namespace autoresearch \
  --upstream autoresearch-worker \
  --port 8080 \
  --health-path /health \
  --wallet 0xYourWalletAddress \
  --chain base-sepolia \
  --per-hour 0.50 \
  --path /services/autoresearch-worker \
  --register \
  --register-name "GPU Worker Alpha" \
  --register-description "A GPU worker for paid autoresearch experiments" \
  --register-skills machine_learning/model_optimization \
  --register-domains technology/artificial_intelligence/research
```
