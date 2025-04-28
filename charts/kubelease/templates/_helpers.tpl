{{/*
Expand the name of the chart.
*/}}
{{- define "kubelease.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kubelease.fullname" -}}
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

{{- define "kubelease.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "kubelease.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "kubelease.controller.fullname" -}}
{{ include "kubelease.fullname" . }}-controller
{{- end }}

{{- define "kubelease.webhook.fullname" -}}
{{ include "kubelease.fullname" . }}-webhook
{{- end }}

{{- define "kubelease.controller.serviceAccountName" -}}
{{- if .Values.controller.serviceAccount.name }}
{{- .Values.controller.serviceAccount.name }}
{{- else }}
{{- include "kubelease.controller.fullname" . }}
{{- end }}
{{- end }}

{{- define "kubelease.webhook.serviceAccountName" -}}
{{- if .Values.webhook.serviceAccount.name }}
{{- .Values.webhook.serviceAccount.name }}
{{- else }}
{{- include "kubelease.webhook.fullname" . }}
{{- end }}
{{- end }}

{{- define "kubelease.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}
