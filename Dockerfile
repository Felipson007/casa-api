# Build context is the shared parent directory of both repos, checked out side by side as
# `casa-api/` (this repo) and `casa-app/` (the frontend):
#   docker build -f casa-api/Dockerfile -t casa-api .
# Paths below are relative to that parent, not to this Dockerfile's own directory.

# Stage 1: static export of the frontend SPA (no Node process at runtime — see next.config.mjs for
# the NEXT_OUTPUT_EXPORT=1 / rewrites() split, same mechanism the Java app already uses).
FROM node:20-alpine AS frontend-build
# SHA do commit deste repo (o backend), passado por publish.yml via --build-arg. O frontend não tem
# como descobri-lo sozinho e o inlinha no bundle para o rodapé exibir a versão do backend ao lado da
# do frontend (ver resolveBackendVersion em app-frontend-atlas/next.config.mjs).
ARG BACKEND_VERSION=dev
WORKDIR /frontend
COPY casa-app/package.json casa-app/package-lock.json ./
RUN npm ci
COPY casa-app/ .
ENV NEXT_OUTPUT_EXPORT=1
ENV NEXT_PUBLIC_BACKEND_VERSION=${BACKEND_VERSION}
RUN npm run build

# Stage 2: compile the API and worker binaries. CGO_ENABLED=0 — every dependency here (pgx, fpdf,
# excelize, oauth2) is pure Go, so a fully static binary needs no libc at runtime, letting the
# final stage be a minimal image instead of needing glibc/musl matched to the build environment.
FROM golang:1.25-alpine AS build
WORKDIR /workspace
COPY casa-api/go.mod casa-api/go.sum ./
RUN go mod download
COPY casa-api/cmd/ ./cmd/
COPY casa-api/internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker
# drive-migrate is an admin one-off: moves a tenant's existing local uploads to its Google Drive
# (see cmd/drive-migrate). Shipped in the image so it can run via `kubectl exec` inside atlas-api,
# reusing the pod's env (DATABASE_URL, TENANT_MASTER_KEY, Google creds) and the mounted uploads volume.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/drive-migrate ./cmd/drive-migrate

# Stage 3: minimal runtime. Alpine (not distroless) deliberately — gives `docker exec sh` for live
# debugging on the homelab, which matches how this app is actually operated (SSH + docker exec, not
# a fully hands-off container). ca-certificates is real: the integrations module makes outbound
# HTTPS calls (Google OAuth token exchange, Drive API) — without it those fail TLS verification.
# postgresql16-client provides pg_dump (major version matched to the postgres:16-alpine server
# image in docker-compose*.yml) — cmd/worker shells out to it for the backup_configuration
# schedule. One image holds both binaries; docker-compose picks which one runs per service via
# `command:`, so there's no separate publish/tag to keep in sync. restic + rclone back the
# consolidated /DATA sweep (cmd/worker's runResticSweep) — rclone isn't invoked directly, restic's
# own `rclone:` backend shells out to it internally for the Google Drive remote.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates postgresql16-client restic rclone
RUN addgroup -S atlas && adduser -S atlas -G atlas
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /out/drive-migrate /app/drive-migrate
COPY --from=frontend-build /frontend/out /app/frontend
RUN mkdir -p /app/uploads /app/backups && chown -R atlas:atlas /app
USER atlas
EXPOSE 8080
ENV APP_STATIC_DIR=/app/frontend
ENV APP_UPLOADS_DIR=/app/uploads
ENV APP_BACKUPS_DIR=/app/backups
ENTRYPOINT ["/app/api"]
