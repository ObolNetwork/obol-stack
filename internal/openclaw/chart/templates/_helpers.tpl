{{/*
Expand the name of the chart.
*/}}
{{- define "openclaw.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "openclaw.fullname" -}}
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
{{- define "openclaw.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "openclaw.labels" -}}
helm.sh/chart: {{ include "openclaw.chart" . }}
{{ include "openclaw.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "openclaw.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openclaw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "openclaw.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "openclaw.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Compute the full image reference.
*/}}
{{- define "openclaw.image" -}}
{{- $tag := .Values.image.tag -}}
{{- if not $tag -}}
{{- $tag = .Chart.AppVersion -}}
{{- end -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the Secret used for envFrom.
*/}}
{{- define "openclaw.secretsName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else if .Values.secrets.name -}}
{{- .Values.secrets.name -}}
{{- else -}}
{{- printf "%s-secrets" (include "openclaw.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Name of the ConfigMap containing openclaw.json.
*/}}
{{- define "openclaw.configMapName" -}}
{{- if .Values.config.existingConfigMap -}}
{{- .Values.config.existingConfigMap -}}
{{- else -}}
{{- printf "%s-config" (include "openclaw.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Name of the PVC used for state storage.
*/}}
{{- define "openclaw.pvcName" -}}
{{- if .Values.persistence.existingClaim -}}
{{- .Values.persistence.existingClaim -}}
{{- else -}}
{{- printf "%s-data" (include "openclaw.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Compute (or reuse) the gateway token value.
*/}}
{{- define "openclaw.gatewayTokenValue" -}}
{{- if .Values.secrets.gatewayToken.value -}}
{{- .Values.secrets.gatewayToken.value -}}
{{- else -}}
{{- $secretName := include "openclaw.secretsName" . -}}
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
Render openclaw.json as strict JSON. If config.content is provided, it is used verbatim.
*/}}
{{- define "openclaw.configJson" -}}
{{- if .Values.config.content -}}
{{- .Values.config.content -}}
{{- else -}}
{{- $gatewayAuth := dict "mode" .Values.openclaw.gateway.auth.mode -}}
{{- if ne .Values.openclaw.gateway.auth.mode "none" -}}
{{- $_ := set $gatewayAuth "token" (printf "${%s}" .Values.secrets.gatewayToken.key) -}}
{{- end -}}

{{- $gateway := dict
  "mode" .Values.openclaw.gateway.mode
  "bind" .Values.openclaw.gateway.bind
  "port" .Values.service.port
  "auth" $gatewayAuth
  "http" (dict "endpoints" (dict "chatCompletions" (dict "enabled" .Values.openclaw.gateway.http.endpoints.chatCompletions.enabled)))
-}}

{{- $agentDefaults := dict "workspace" .Values.openclaw.workspaceDir -}}
{{- if .Values.openclaw.agentModel -}}
{{- $_ := set $agentDefaults "model" (dict "primary" .Values.openclaw.agentModel) -}}
{{- end -}}

{{- $cfg := dict
  "gateway" $gateway
  "agents" (dict "defaults" $agentDefaults)
-}}

{{- if .Values.skills.enabled -}}
{{- $_ := set $cfg "skills" (dict "load" (dict
  "extraDirs" (list .Values.skills.extractDir)
)) -}}
{{- end -}}

{{- /* Build providers map from all enabled model providers */ -}}
{{- $providers := dict -}}
{{- range $name := list "anthropic" "openai" "ollama" -}}
{{- $p := index $.Values.models $name -}}
{{- if $p.enabled -}}
{{- $models := list -}}
{{- range $m := $p.models -}}
{{- $models = append $models (dict "id" $m.id "name" $m.name) -}}
{{- end -}}
{{- $entry := dict
  "baseUrl" $p.baseUrl
  "apiKey" (printf "${%s}" $p.apiKeyEnvVar)
  "models" $models
-}}
{{- if $p.api -}}
{{- $_ := set $entry "api" $p.api -}}
{{- end -}}
{{- $_ := set $providers $name $entry -}}
{{- end -}}
{{- end -}}
{{- if $providers -}}
{{- $_ := set $cfg "models" (dict "providers" $providers) -}}
{{- end -}}

{{- /* Build channels config from enabled integrations */ -}}
{{- $channels := dict -}}
{{- if .Values.channels.telegram.enabled -}}
{{- $tg := dict "botToken" (printf "${TELEGRAM_BOT_TOKEN}") -}}
{{- if .Values.channels.telegram.dmPolicy -}}
{{- $_ := set $tg "dmPolicy" .Values.channels.telegram.dmPolicy -}}
{{- end -}}
{{- $_ := set $channels "telegram" $tg -}}
{{- end -}}
{{- if .Values.channels.discord.enabled -}}
{{- $dc := dict "botToken" (printf "${DISCORD_BOT_TOKEN}") -}}
{{- if .Values.channels.discord.dmPolicy -}}
{{- $_ := set $dc "dmPolicy" .Values.channels.discord.dmPolicy -}}
{{- end -}}
{{- $_ := set $channels "discord" $dc -}}
{{- end -}}
{{- if .Values.channels.slack.enabled -}}
{{- $sl := dict "botToken" (printf "${SLACK_BOT_TOKEN}") "appToken" (printf "${SLACK_APP_TOKEN}") -}}
{{- $_ := set $channels "slack" $sl -}}
{{- end -}}
{{- if $channels -}}
{{- $_ := set $cfg "channels" $channels -}}
{{- end -}}

{{- $cfg | toPrettyJson -}}
{{- end -}}
{{- end }}

{{/*
Name of the skills ConfigMap (user-provided or chart-created default).
*/}}
{{- define "openclaw.skillsConfigMapName" -}}
{{- if .Values.skills.configMapName -}}
{{- .Values.skills.configMapName -}}
{{- else -}}
{{- printf "%s-skills" (include "openclaw.fullname" .) -}}
{{- end -}}
{{- end }}
