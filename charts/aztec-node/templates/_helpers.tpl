{{/*
Expand the name of the chart.
*/}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "chart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "chart.labels" -}}
helm.sh/chart: {{ include "chart.chart" . }}
{{ include "chart.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}


{{/*
Create the name of the service account to use
*/}}
{{- define "chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "chart.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the cluster role.
It needs to be namespace prefixed to avoid naming conflicts when using the same deployment name across namespaces.
*/}}
{{- define "chart.clusterRoleName" -}}
{{ .Release.Namespace }}-{{ include "chart.fullname" . }}
{{- end }}

{{/*
Validate that the role is one of the allowed values
*/}}
{{- define "chart.validateRole" -}}
{{- $validRoles := list "fullnode" "sequencer" "prover" -}}
{{- if not (has .Values.role $validRoles) -}}
{{- fail (printf "Invalid role '%s'. Must be one of: %s" .Values.role (join ", " $validRoles)) -}}
{{- end -}}
{{- end -}}

{{/*
Validate sequencer configuration
*/}}
{{- define "chart.validateSequencer" -}}
{{- if eq .Values.role "sequencer" -}}
{{- if not .Values.sequencer.attesterPrivateKey -}}
{{- fail "sequencer.attesterPrivateKey is REQUIRED when role is 'sequencer'" -}}
{{- end -}}
{{- if not (hasPrefix "0x" .Values.sequencer.attesterPrivateKey) -}}
{{- fail "sequencer.attesterPrivateKey must start with '0x'" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Validate prover configuration
*/}}
{{- define "chart.validateProver" -}}
{{- if eq .Values.role "prover" -}}
{{- if not .Values.prover.node.publisherPrivateKey -}}
{{- fail "prover.node.publisherPrivateKey is REQUIRED when role is 'prover'" -}}
{{- end -}}
{{- if not (hasPrefix "0x" .Values.prover.node.publisherPrivateKey) -}}
{{- fail "prover.node.publisherPrivateKey must start with '0x'" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Generate startCmd based on role (if not overridden)
Only used for fullnode and sequencer roles
Prover role uses separate component-specific commands
*/}}
{{- define "chart.startCmd" -}}
{{- if .Values.node.startCmd -}}
{{- .Values.node.startCmd | toYaml -}}
{{- else -}}
{{- if eq .Values.role "fullnode" }}
- --node
- --archiver
{{- else if eq .Values.role "sequencer" }}
- --node
- --archiver
- --sequencer
{{- end }}
{{- if .Values.network }}
- --network
- {{ .Values.network }}
{{- end }}
{{- end -}}
{{- end -}}

{{/*
Run all validations
*/}}
{{- define "chart.validate" -}}
{{- include "chart.validateRole" . -}}
{{- include "chart.validateSequencer" . -}}
{{- include "chart.validateProver" . -}}
{{- end -}}
