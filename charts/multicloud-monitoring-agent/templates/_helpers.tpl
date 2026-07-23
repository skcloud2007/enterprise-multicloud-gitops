{{- define "multicloud-monitoring-agent.name" -}}
multicloud-monitoring-agent
{{- end }}

{{- define "multicloud-monitoring-agent.fullname" -}}
{{- printf "%s" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "multicloud-monitoring-agent.labels" -}}
app.kubernetes.io/name: {{ include "multicloud-monitoring-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
platform.local/cluster: {{ required "cluster.name is required" .Values.cluster.name | quote }}
platform.local/cloud: {{ required "cluster.cloud is required" .Values.cluster.cloud | quote }}
platform.local/environment: {{ required "cluster.environment is required" .Values.cluster.environment | quote }}
{{- end }}

{{- define "multicloud-monitoring-agent.serviceAccountName" -}}
{{ include "multicloud-monitoring-agent.fullname" . }}
{{- end }}
