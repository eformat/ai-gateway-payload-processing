{{- define "llm-katan.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "llm-katan.labels" -}}
app.kubernetes.io/name: llm-katan-simulator
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "llm-katan.selectorLabels" -}}
app: llm-katan-simulator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
