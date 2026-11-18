#!/usr/bin/env bash
#
# provision-atlas-builder-lxc.sh — setup unico, rodado NO HOST PROXMOX (root). Cria/atualiza o LXC
# "atlas-builder" (unprivileged, nesting+keyctl pro Docker), instala docker+git+jq, copia o
# atlas-build.sh + units e habilita o atlas-build.timer. Depois disso nada mais e manual: o timer
# builda e publica no GHCR sozinho, e o CronJob atlas-auto-update (k3s) faz o deploy.
#
# Uso (no host Proxmox, a partir de um clone/copia do repo, na pasta homelab/):
#   export GH_TOKEN=<PAT classic com repo + write:packages>
#   sudo -E bash homelab/provision-atlas-builder-lxc.sh
#
# Overrides opcionais (env):
#   CT_ID=110  CT_HOSTNAME=atlas-builder  STORAGE=local-lvm  DISK_GB=16
#   CORES=2  MEMORY_MB=2048  BRIDGE=vmbr0  CT_IP=dhcp
#   TEMPLATE_MATCH=debian-13-standard
#
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

CT_ID="${CT_ID:-110}"
CT_HOSTNAME="${CT_HOSTNAME:-atlas-builder}"
STORAGE="${STORAGE:-local-lvm}"
DISK_GB="${DISK_GB:-16}"
CORES="${CORES:-2}"
MEMORY_MB="${MEMORY_MB:-2048}"
BRIDGE="${BRIDGE:-vmbr0}"
CT_IP="${CT_IP:-dhcp}"
TEMPLATE_MATCH="${TEMPLATE_MATCH:-debian-13-standard}"

# Arquitetura do host (Proxmox usa amd64/arm64). Sem o filtro, 'pveam available' lista amd64 E
# arm64 e o sort -V escolhe arm64 por engano (o container nao sobe em host x86_64).
ARCH="$(dpkg --print-architecture 2>/dev/null || true)"
if [ -z "$ARCH" ]; then
  case "$(uname -m)" in
    x86_64)  ARCH=amd64 ;;
    aarch64) ARCH=arm64 ;;
    *)       ARCH="$(uname -m)" ;;
  esac
fi

log() { printf '[provision-atlas-builder] %s\n' "$*"; }
die() { printf '[provision-atlas-builder] erro: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "rode como root (sudo -E ...)"
[ -n "${GH_TOKEN:-}" ] || die "exporte GH_TOKEN=<PAT com repo + write:packages> antes de rodar"
for f in atlas-build.sh atlas-build.service atlas-build.timer; do
  [ -f "$here/$f" ] || die "arquivo $f nao encontrado em $here (rode a partir da pasta homelab/ do repo)"
done
command -v pct >/dev/null || die "pct nao encontrado — este script roda no host Proxmox"
command -v pveam >/dev/null || die "pveam nao encontrado — este script roda no host Proxmox"

# 1) Garante o template LXC Debian.
pveam update >/dev/null
tpl="$(pveam available --section system | awk -v m="$TEMPLATE_MATCH" -v a="$ARCH" '$2 ~ m && $2 ~ a { print $2 }' | sort -V | tail -n1)"
[ -n "$tpl" ] || die "nenhum template LXC '$TEMPLATE_MATCH' para $ARCH em 'pveam available --section system'"
if ! pveam list local 2>/dev/null | grep -q "$tpl"; then
  log "baixando template LXC $tpl"
  pveam download local "$tpl"
else
  log "template LXC $tpl ja presente"
fi

# 2) Cria o container se nao existir.
if pct status "$CT_ID" >/dev/null 2>&1; then
  log "LXC $CT_ID ja existe — reaproveitando (nao recria, sem destruir nada)"
else
  log "criando LXC $CT_ID ($CT_HOSTNAME)"
  pct create "$CT_ID" "local:vztmpl/$tpl" \
    --hostname "$CT_HOSTNAME" \
    --cores "$CORES" \
    --memory "$MEMORY_MB" \
    --swap 512 \
    --rootfs "$STORAGE:$DISK_GB" \
    --net0 "name=eth0,bridge=$BRIDGE,ip=$CT_IP" \
    --features "nesting=1,keyctl=1" \
    --unprivileged 1 \
    --onboot 1
fi

pct status "$CT_ID" | grep -q running || { log "iniciando LXC"; pct start "$CT_ID"; }
log "aguardando o LXC responder ..."
for _ in $(seq 1 30); do
  pct exec "$CT_ID" -- true >/dev/null 2>&1 && break
  sleep 2
done
pct exec "$CT_ID" -- true >/dev/null 2>&1 || die "LXC $CT_ID nao respondeu via pct exec"

# 3) Dependencias + Docker (idempotente).
log "instalando git/curl/jq dentro do LXC"
pct exec "$CT_ID" -- bash -lc 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq git curl jq ca-certificates'
if ! pct exec "$CT_ID" -- sh -c 'command -v docker' >/dev/null 2>&1; then
  log "instalando Docker dentro do LXC"
  pct exec "$CT_ID" -- bash -lc 'curl -fsSL https://get.docker.com | sh'
fi

# 4) Copia o builder + units e habilita o timer.
log "instalando o builder no LXC"
pct push "$CT_ID" "$here/atlas-build.sh" /usr/local/bin/atlas-build.sh --perms 755
pct push "$CT_ID" "$here/atlas-build.service" /etc/systemd/system/atlas-build.service --perms 644
pct push "$CT_ID" "$here/atlas-build.timer" /etc/systemd/system/atlas-build.timer --perms 644

# Env (0600) escrito via arquivo temporario no host — evita o token aparecer em argumentos de comando.
tmp_env="$(mktemp)"; trap 'rm -f "$tmp_env"' EXIT
umask 077
printf 'GH_TOKEN=%s\n' "$GH_TOKEN" > "$tmp_env"
pct push "$CT_ID" "$tmp_env" /etc/atlas-build.env --perms 600

pct exec "$CT_ID" -- systemctl daemon-reload
pct exec "$CT_ID" -- systemctl enable --now atlas-build.timer
# Primeiro build imediatamente (nao bloqueia o provisionamento).
pct exec "$CT_ID" -- systemctl start --no-block atlas-build.service

log "ok — timer ativo (2 min) no LXC $CT_ID ($CT_HOSTNAME)."
log "acompanhar: pct exec $CT_ID -- journalctl -fu atlas-build.service"
