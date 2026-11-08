{{/* Метки, общие для всех объектов чарта. */}}
{{- define "gophprofile.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/part-of: gophprofile
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Образ сервиса с тегом из values. */}}
{{- define "gophprofile.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/* Ограничения уровня restricted для пода. */}}
{{- define "gophprofile.podSecurity" -}}
runAsNonRoot: true
runAsUser: {{ . }}
runAsGroup: {{ . }}
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{/* Ограничения уровня restricted для контейнера. */}}
{{- define "gophprofile.containerSecurity" -}}
allowPrivilegeEscalation: false
capabilities:
  drop: ["ALL"]
{{- end }}

{{/* Переменные окружения server и worker. */}}
{{- define "gophprofile.env" -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: avatar-secrets
      key: database-url
- name: S3_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: avatar-secrets
      key: s3-access-key
- name: S3_SECRET_KEY
  valueFrom:
    secretKeyRef:
      name: avatar-secrets
      key: s3-secret-key
- name: RABBITMQ_URL
  valueFrom:
    secretKeyRef:
      name: avatar-secrets
      key: rabbitmq-url
{{- range $env, $key := dict "S3_ENDPOINT" "s3_endpoint" "S3_BUCKET" "s3_bucket" "MAX_FILE_SIZE" "max_file_size" "ALLOWED_MIME_TYPES" "allowed_mime_types" "LOG_LEVEL" "log_level" "TRUSTED_PROXIES" "trusted_proxies" "OTEL_EXPORTER_OTLP_ENDPOINT" "otel_endpoint" }}
- name: {{ $env }}
  valueFrom:
    configMapKeyRef:
      name: avatar-config
      key: {{ $key }}
{{- end }}
- name: OTEL_TRACES_EXPORTER
  value: none
- name: OTEL_LOGS_EXPORTER
  value: none
- name: OTEL_METRIC_EXPORT_INTERVAL
  value: "5000"
- name: GOMEMLIMIT
  valueFrom:
    resourceFieldRef:
      resource: limits.memory
      divisor: "1"
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_RESOURCE_ATTRIBUTES
  value: service.instance.id=$(POD_NAME)
{{- end }}
