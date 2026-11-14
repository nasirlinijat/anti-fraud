{{- define "antifraud.labels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: anti-fraud
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end }}

{{- define "antifraud.selector" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end }}

{{- define "antifraud.image" -}}
{{- $image := index .root.Values.images .component -}}
{{- printf "%s:%s" $image.repository (default .root.Values.images.tag $image.tag) -}}
{{- end }}

{{- define "antifraud.secretName" -}}
{{- default (printf "%s-secrets" .Release.Name) .Values.existingSecret -}}
{{- end }}

{{- define "antifraud.secretRef" -}}
valueFrom:
  secretKeyRef:
    name: {{ include "antifraud.secretName" .root }}
    key: {{ .key }}
    {{- if .optional }}
    optional: true
    {{- end }}
{{- end }}

{{- define "antifraud.checksum" -}}
checksum/secrets: {{ printf "%s%s" (include (print .Template.BasePath "/secret.yaml") .) .Values.secretsChecksum | sha256sum }}
{{- end }}

{{- define "antifraud.datadogLabels" -}}
{{- if .root.Values.datadog.enabled }}
tags.datadoghq.com/env: {{ .root.Values.datadog.env | quote }}
tags.datadoghq.com/service: {{ .service | quote }}
{{- with .version }}
tags.datadoghq.com/version: {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}

{{- define "antifraud.datadogLogs" -}}
{{- if .root.Values.datadog.enabled }}
ad.datadoghq.com/{{ .container }}.logs: {{ printf "[{\"source\": %q, \"service\": %q}]" .source .service | squote }}
{{- end }}
{{- end }}
