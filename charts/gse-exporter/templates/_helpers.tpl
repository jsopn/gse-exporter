{{- define "gse-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gse-exporter.fullname" -}}
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

{{- define "gse-exporter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gse-exporter.labels" -}}
helm.sh/chart: {{ include "gse-exporter.chart" . }}
{{ include "gse-exporter.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "gse-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gse-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gse-exporter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gse-exporter.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "gse-exporter.telemetryPath" -}}
{{- default "/metrics" .Values.config.telemetryPath }}
{{- end }}

{{- define "gse-exporter.args" -}}
{{- $c := .Values.config -}}
- -web.listen-address=:{{ .Values.service.port }}
- -web.telemetry-path={{ include "gse-exporter.telemetryPath" . }}
{{- with $c.baseURL }}
- -gse.base-url={{ . }}
{{- end }}
- -poll.interval={{ $c.pollInterval }}
- -poll.timeout={{ $c.pollTimeout }}
- -log.level={{ $c.logLevel }}
{{- with .Values.extraArgs }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "gse-exporter.ruleGroups" -}}
{{- $r := .Values.rules }}
groups:
  - name: {{ include "gse-exporter.fullname" . }}
    rules:
{{- if $r.consumptionCollapse.enabled }}
      - alert: GSEConsumptionCollapse
        expr: gse_consumption_watts / (gse_consumption_watts offset {{ $r.consumptionCollapse.window }}) < {{ $r.consumptionCollapse.ratio }}
        for: {{ $r.consumptionCollapse.for }}
        labels:
          severity: {{ $r.consumptionCollapse.severity }}
        annotations:
          summary: Georgian grid is shedding load
          description: >-
            Country-wide consumption is {{ `{{ $value | humanizePercentage }}` }} of what it was
            {{ $r.consumptionCollapse.window }} ago. A drop this steep is load shedding or a
            nation-wide outage, not a normal ramp.
{{- end }}
{{- if $r.consumptionBelowPlan.enabled }}
      - alert: GSEConsumptionBelowPlan
        expr: gse_consumption_watts / gse_consumption_estimate_watts < {{ $r.consumptionBelowPlan.ratio }}
        for: {{ $r.consumptionBelowPlan.for }}
        labels:
          severity: {{ $r.consumptionBelowPlan.severity }}
        annotations:
          summary: Georgian consumption is far below GSE's own plan
          description: >-
            Country-wide consumption has been {{ `{{ $value | humanizePercentage }}` }} of the load
            GSE planned for this sample for {{ $r.consumptionBelowPlan.for }}, so demand that
            should be there is going unserved.
{{- end }}
{{- if $r.exporterDown.enabled }}
      - alert: GSEExporterDown
        expr: gse_up == 0
        for: {{ $r.exporterDown.for }}
        labels:
          severity: {{ $r.exporterDown.severity }}
        annotations:
          summary: gse-exporter cannot reach the GSE API
          description: >-
            {{ `{{ $labels.instance }}` }} has failed every poll of the GSE API for {{ $r.exporterDown.for }}.
{{- end }}
{{- if $r.dataStale.enabled }}
      - alert: GSEDataStale
        expr: time() - gse_data_timestamp_seconds > {{ $r.dataStale.maxAgeSeconds }}
        for: {{ $r.dataStale.for }}
        labels:
          severity: {{ $r.dataStale.severity }}
        annotations:
          summary: GSE grid data has stopped updating
          description: >-
            Newest sample on {{ `{{ $labels.instance }}` }} is
            {{ `{{ $value | humanizeDuration }}` }} old, upstream normally samples every 3 minutes.
{{- end }}
{{- if $r.frequencyDeviation.enabled }}
      - alert: GSEFrequencyDeviation
        expr: abs(gse_frequency_hertz - 50) > {{ $r.frequencyDeviation.deviationHz }}
        for: {{ $r.frequencyDeviation.for }}
        labels:
          severity: {{ $r.frequencyDeviation.severity }}
        annotations:
          summary: Georgian grid frequency is off nominal
          description: >-
            Grid frequency is {{ `{{ $value | printf "%.2f" }}` }} Hz, more than
            {{ $r.frequencyDeviation.deviationHz }} Hz off the nominal 50 Hz.
{{- end }}
{{- with $r.additional }}
{{ toYaml . | indent 6 }}
{{- end }}
{{- end }}
