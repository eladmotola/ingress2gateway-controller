{{/*
Expand the name of the chart.
*/}}
{{- define "ingress2gateway-controller.name" -}}
{{- default .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "ingress2gateway-controller.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s" $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ingress2gateway-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ingress2gateway-controller.labels" -}}
helm.sh/chart: {{ include "ingress2gateway-controller.chart" . }}
{{ include "ingress2gateway-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ingress2gateway-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ingress2gateway-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: ingress2gateway-controller
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ingress2gateway-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ingress2gateway-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the image string
*/}}
{{- define "ingress2gateway-controller.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository $tag }}
{{- end }}
