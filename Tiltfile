# Tiltfile: hot-reload local dev against the k3d cluster, running the SAME Helm chart prod runs
# (deploy/chart + values.dev.yaml). The fast native loop (`make dev`) stays separate.
#
#   ./deploy/dev/k3d-up.sh   # once
#   tilt up                  # edit a .go file -> rebuilt binary syncs into the pod + restarts

load('ext://restart_process', 'docker_build_with_restart')

# Only ever run against the local dev cluster.
allow_k8s_contexts('k3d-vig-dev')
if k8s_context() != 'k3d-vig-dev':
    fail("wrong context %r - run ./deploy/dev/k3d-up.sh first (expects k3d-vig-dev)" % k8s_context())

# k3d's registry: localhost:5006 from the host, vig-registry:5000 from pods.
default_registry('localhost:5006', host_from_cluster='vig-registry:5000')

# --- Host-built artifacts (no toolchain in the dev image) ---
ldflags = '-X github.com/forefy/vig/backend/internal/api.Version=$(git describe --tags --always 2>/dev/null || echo dev)-dev ' + \
          '-X github.com/forefy/vig/backend/internal/api.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)'
server_build = 'cd backend && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "%s" -o bin/vig ./cmd/vig' % ldflags
# Blocking at eval so backend/bin/vig exists before the first image build.
local(server_build, quiet=True)
local_resource(
    'backend-compile',
    server_build,
    deps=['backend/cmd', 'backend/internal', 'backend/go.mod'],
    ignore=['backend/bin'],
    labels=['backend'],
)

web_build = 'cd frontend && npm run build'
if not os.path.exists('frontend/dist/index.html'):
    local(web_build, quiet=True)
local_resource(
    'frontend-build',
    web_build,
    deps=['frontend/src', 'frontend/index.html', 'frontend/vite.config.ts', 'frontend/scripts'],
    ignore=['frontend/dist', 'frontend/node_modules'],
    labels=['frontend'],
)

# --- Dev image + live_update ---
docker_build_with_restart(
    'vig',
    context='.',
    dockerfile='deploy/dev/Dockerfile.dev',
    entrypoint=['/app/vig'],
    only=['backend/bin/vig', 'frontend/dist'],
    # The chart mounts emptyDirs over /tmp and /data; keep the restart marker in /app.
    restart_file='/app/.restart-proc',
    live_update=[
        sync('backend/bin/vig', '/app/vig'),
        sync('frontend/dist', '/srv/web'),
    ],
)

# --- Deploy the prod chart with dev values ---
k8s_yaml(helm(
    'deploy/chart',
    name='vig',
    values=['deploy/chart/values.dev.yaml'],
    set=[
        'fullnameOverride=vig',
        'image.repository=vig',
        'image.tag=dev',
        'image.pullPolicy=IfNotPresent',
        # Dev image runs writable/root so live_update can sync into /app and /srv.
        'podSecurityContext.runAsNonRoot=false',
        'podSecurityContext.runAsUser=0',
        'securityContext.readOnlyRootFilesystem=false',
    ],
))

k8s_resource('vig-postgres', labels=['data'])
k8s_resource('vig-migrate', resource_deps=['vig-postgres'], labels=['data'])
k8s_resource(
    'vig',
    resource_deps=['vig-postgres', 'backend-compile', 'frontend-build'],
    port_forwards=['7112:7112'],
    labels=['backend'],
)
