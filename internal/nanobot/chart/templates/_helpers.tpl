{{/*
Expand the name of the chart.
*/}}
{{- define "nanobot.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "nanobot.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "nanobot.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "nanobot.labels" -}}
helm.sh/chart: {{ include "nanobot.chart" . }}
{{ include "nanobot.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "nanobot.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nanobot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "nanobot.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nanobot.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Compute the full image reference.
*/}}
{{- define "nanobot.image" -}}
{{- $tag := .Values.image.tag -}}
{{- if not $tag -}}
{{- $tag = .Chart.AppVersion -}}
{{- end -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the Secret used for envFrom.
*/}}
{{- define "nanobot.secretsName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else if .Values.secrets.name -}}
{{- .Values.secrets.name -}}
{{- else -}}
{{- printf "%s-secrets" (include "nanobot.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Name of the ConfigMap containing config.json.
*/}}
{{- define "nanobot.configMapName" -}}
{{- if .Values.config.existingConfigMap -}}
{{- .Values.config.existingConfigMap -}}
{{- else -}}
{{- printf "%s-config" (include "nanobot.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Name of the PVC used for state storage.
*/}}
{{- define "nanobot.pvcName" -}}
{{- if .Values.persistence.existingClaim -}}
{{- .Values.persistence.existingClaim -}}
{{- else -}}
{{- printf "%s-data" (include "nanobot.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Compute (or reuse) the gateway token value.
*/}}
{{- define "nanobot.gatewayTokenValue" -}}
{{- if .Values.secrets.gatewayToken.value -}}
{{- .Values.secrets.gatewayToken.value -}}
{{- else -}}
{{- $secretName := include "nanobot.secretsName" . -}}
{{- $key := .Values.secrets.gatewayToken.key -}}
{{- $existing := (lookup "v1" "Secret" .Release.Namespace $secretName) -}}
{{- if $existing -}}
  {{- $data := index $existing "data" -}}
  {{- if and $data (hasKey $data $key) -}}
    {{- index $data $key | b64dec -}}
  {{- else -}}
    {{- randAlphaNum 48 -}}
  {{- end -}}
{{- else -}}
  {{- randAlphaNum 48 -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Render config.json for Nanobot. If config.content is provided, it is used verbatim.
*/}}
{{- define "nanobot.configJson" -}}
{{- if .Values.config.content -}}
{{- .Values.config.content -}}
{{- else -}}
{{- $cfg := dict
  "gateway" (dict
    "host" .Values.nanobot.gateway.host
    "port" (.Values.nanobot.gateway.port | int)
    "auth" (dict "token" (printf "${%s}" .Values.secrets.gatewayToken.key))
  )
  "stateDir" .Values.nanobot.stateDir
  "workspaceDir" .Values.nanobot.workspaceDir
-}}

{{- if .Values.nanobot.agentModel -}}
{{- $_ := set $cfg "agentModel" .Values.nanobot.agentModel -}}
{{- end -}}

{{- /* Build providers map from all enabled providers */ -}}
{{- $providers := dict -}}
{{- range $name := list "anthropic" "openai" "openrouter" "ollama" -}}
{{- $p := index $.Values.providers $name -}}
{{- if $p.enabled -}}
{{- $entry := dict
  "baseUrl" $p.baseUrl
  "apiKey" (printf "${%s}" $p.apiKeyEnvVar)
-}}
{{- $_ := set $providers $name $entry -}}
{{- end -}}
{{- end -}}
{{- if $providers -}}
{{- $_ := set $cfg "providers" $providers -}}
{{- end -}}

{{- /* Build channels config from enabled integrations */ -}}
{{- $channels := dict -}}
{{- if .Values.channels.telegram.enabled -}}
{{- $_ := set $channels "telegram" (dict "token" "${TELEGRAM_BOT_TOKEN}") -}}
{{- end -}}
{{- if .Values.channels.discord.enabled -}}
{{- $_ := set $channels "discord" (dict "token" "${DISCORD_BOT_TOKEN}") -}}
{{- end -}}
{{- if $channels -}}
{{- $_ := set $cfg "channels" $channels -}}
{{- end -}}

{{- $cfg | toPrettyJson -}}
{{- end -}}
{{- end }}
