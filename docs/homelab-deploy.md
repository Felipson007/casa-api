# Deploy — homelab (k3s + ArgoCD)

> **Produção roda em Proxmox VE + k3s.** Os manifests e o runbook vivem no repo
> [`gustavodvp/homelab`](https://github.com/gustavodvp/homelab) — este documento aqui é só um
> resumo do que toca o atlas-go.

## Publicação da imagem

O build **não roda no GitHub Actions**: [`homelab/atlas-build.sh`](../homelab/atlas-build.sh)
(rodando num LXC dedicado no Proxmox, via `atlas-build.timer` a cada 2 min) compila o backend, faz
o export estático do frontend como stage da MESMA imagem (SPA embutido, ver
[`Dockerfile`](../Dockerfile)) e publica `ghcr.io/gustavodvp/app-golang-atlas:latest`
(+ tag `:<sha-do-backend>`) no GHCR. Ele só builda quando o `main` de qualquer um dos dois repos
avança **e** o workflow "CI" do commit correspondente terminou com sucesso.

Setup único (no host Proxmox): [`homelab/provision-atlas-builder-lxc.sh`](../homelab/provision-atlas-builder-lxc.sh).
Substitui o antigo `publish.yml`, removido.

## Deploy no cluster

- Manifests: `gustavodvp/homelab` → `kubernetes/applications/atlas/` — `api.yaml`, `worker.yaml`,
  `postgres.yaml`, `application.yaml` (o `Application` do ArgoCD) e `auto-update.yaml`.
- ArgoCD sincroniza esses manifests (GitOps); os Secrets são criados fora do Git.
- **Auto-update:** `CronJob/atlas-auto-update` (a cada 5 min) compara o digest publicado no GHCR
  com o que está rodando e reinicia `atlas-api`/`atlas-worker` quando muda.
- Recuperação e operação: `gustavodvp/homelab` → `docs/disaster-recovery/DISASTER_RECOVERY.md`.

## Alternativa local (docker compose)

Para subir sem cluster: copie `.env.example` para `.env`, preencha as variáveis e rode
`docker compose -f docker-compose.homelab.yml up -d` (ver o [README](../README.md)).
