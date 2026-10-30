{{/* Common name/label helpers. */}}

{{- define "vig.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "vig.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "vig.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "vig.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "vig.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "vig.selectorLabels" -}}
app.kubernetes.io/name: {{ include "vig.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "vig.postgresFullname" -}}
{{- printf "%s-postgres" (include "vig.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Name of the Secret the app reads from: an externally managed one, or the chart-rendered one. */}}
{{- define "vig.secretName" -}}
{{- if eq .Values.secrets.mode "existing" -}}
{{- required "secrets.existingSecret is required when secrets.mode=existing" .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "vig.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Resolve the Postgres password once and reuse it across upgrades: read it back from the existing
Secret if present, otherwise generate a fresh one.
*/}}
{{- define "vig.postgresPassword" -}}
{{- $secretName := printf "%s-secrets" (include "vig.fullname" .) -}}
{{- $existing := lookup "v1" "Secret" .Release.Namespace $secretName -}}
{{- if and $existing (index $existing.data "postgres-password") -}}
{{- index $existing.data "postgres-password" | b64dec -}}
{{- else -}}
{{- randAlphaNum 24 -}}
{{- end -}}
{{- end -}}

{{/* The image reference: digest wins over tag (immutable pulls in prod). */}}
{{- define "vig.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (required "image.tag or image.digest is required" .Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{/* Public origin: app.siteUrl, else scheme + ingress.host. */}}
{{- define "vig.siteUrl" -}}
{{- if .Values.app.siteUrl -}}
{{- .Values.app.siteUrl | trimSuffix "/" -}}
{{- else if .Values.ingress.tls.enabled -}}
{{- printf "https://%s" .Values.ingress.host -}}
{{- else -}}
{{- printf "http://%s" .Values.ingress.host -}}
{{- end -}}
{{- end -}}

{{/* One optional secretKeyRef env entry. */}}
{{- define "vig.secretEnv" -}}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ .secret }}
      key: {{ .key }}
      optional: true
{{- end -}}

{{/* Every environment variable the vig binary reads, shared by the Deployment and the migration Job. */}}
{{- define "vig.env" -}}
{{- $secret := include "vig.secretName" . -}}
{{- $site := include "vig.siteUrl" . -}}
- name: VIG_ENV
  value: prod
- name: PORT
  value: {{ .Values.service.port | quote }}
- name: FRONTEND_URL
  value: {{ $site | quote }}
- name: SITE_URL
  value: {{ $site | quote }}
- name: BACKEND_URL
  value: {{ $site | quote }}
- name: STATIC_DIR
  value: /srv/web
- name: DATA_DIR
  value: /data
- name: LOG_FORMAT
  value: {{ .Values.app.logFormat | quote }}
- name: VIG_WORKERS
  value: {{ ternary "1" "0" .Values.app.workers | quote }}
- name: VIG_WATCH
  value: {{ ternary "1" "0" .Values.app.watch | quote }}
- name: SESSION_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: session-secret
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: database-url
{{- range $k, $v := dict "GOOGLE_CLIENT_ID" "google-client-id" "GOOGLE_CLIENT_SECRET" "google-client-secret" "GITHUB_OAUTH_CLIENT_ID" "github-oauth-client-id" "GITHUB_OAUTH_CLIENT_SECRET" "github-oauth-client-secret" "GITHUB_APP_ID" "github-app-id" "GITHUB_APP_PRIVATE_KEY" "github-app-private-key" "GITHUB_APP_WEBHOOK_SECRET" "github-app-webhook-secret" "DISCORD_CLIENT_ID" "discord-client-id" "DISCORD_CLIENT_SECRET" "discord-client-secret" "DISCORD_BOT_TOKEN" "discord-bot-token" "GITHUB_TOKEN" "github-token" "RPC_PRIMARY_URL" "rpc-primary-url" "SMTP_PASS" "smtp-pass" }}
{{ include "vig.secretEnv" (dict "name" $k "key" $v "secret" $secret) }}
{{- end }}
- name: TREASURY_PUBKEY
  value: {{ .Values.app.treasuryPubkey | quote }}
- name: USDC_MINT
  value: {{ .Values.app.usdcMint | quote }}
- name: RPC_FALLBACK_URL
  value: {{ .Values.app.rpcFallbackUrl | quote }}
- name: OSEC_VERIFY_URL
  value: {{ .Values.app.osecVerifyUrl | quote }}
- name: GITHUB_API_URL
  value: {{ .Values.app.githubApiUrl | quote }}
- name: GITHUB_APP_SLUG
  value: {{ .Values.app.githubAppSlug | quote }}
- name: DISCORD_GUILD_ID
  value: {{ .Values.app.discordGuildId | quote }}
- name: DISCORD_INVITE_URL
  value: {{ .Values.app.discordInviteUrl | quote }}
- name: SMTP_HOST
  value: {{ .Values.app.smtp.host | quote }}
- name: SMTP_PORT
  value: {{ .Values.app.smtp.port | quote }}
- name: SMTP_USER
  value: {{ .Values.app.smtp.user | quote }}
- name: SMTP_FROM
  value: {{ .Values.app.smtp.from | quote }}
{{- with .Values.app.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}
