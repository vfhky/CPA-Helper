# CPA-Helper CI/CD Pipeline — Architecture Design

**Date:** 2026-07-05
**Status:** Draft
**Reference:** CLIProxyAPI `.github/workflows/` (sync-upstream.yml, docker-build.yml) and `dockerHub/projects/cliproxyapi-watcher/`

---

## 1. Goal

Automate upstream sync, Docker image build/push, and production container update for CPA-Helper, matching CLIProxyAPI's existing CI/CD pattern.

## 2. Architecture Overview

```
walkingddd/CPA-Helper main (upstream)
        │
        ▼ (daily cron + manual dispatch)
┌─ sync-upstream.yml ───────────────────────────────┐
│  git merge upstream/main → feat/api-key-entries   │
│  git push vfhky/CPA-Helper feat/api-key-entries   │
│  Telegram notify (success/failure)                │
└───────────────────────────────────────────────────┘
        │ push event triggers
        ▼
┌─ docker-build.yml ────────────────────────────────┐
│  Multi-arch build (amd64 + arm64)                 │
│  → ghcr.io/vfhky/cpa-helper                       │
│  → docker.io/vfhky/cpa-helper                     │
│  → ccr.ccs.tencentyun.com/typecodes/cpa-helper    │
│  Telegram notify (success/failure)                │
└───────────────────────────────────────────────────┘
        │ new image available
        ▼
┌─ cpah-watcher (Watchtower) ──────────────────────┐
│  Poll every 300s for cpah-cpah-1 container       │
│  Auto pull + restart on new image                │
│  Telegram notify                                 │
└───────────────────────────────────────────────────┘
```

## 3. Files Changed

| Repository | File | Action |
|-----------|------|--------|
| CPA-Helper | `.github/workflows/sync-upstream.yml` | Create |
| CPA-Helper | `.github/workflows/docker-build.yml` | Create |
| dockerHub | `projects/cpah-watcher/project.yaml` | Create |
| dockerHub | `projects/cpah-watcher/compose.yaml` | Create |
| dockerHub | `projects/cpah-watcher/.env` | Create |
| dockerHub | `projects/cpah/.env` | Modify — `CPAH_IMAGE` changed to TCR registry |

## 4. GitHub Secrets Required

All secrets already exist in vfhky/CLIProxyAPI and are shared:

| Secret | Used By |
|--------|---------|
| `GHCR_TOKEN` | sync-upstream (push), docker-build (ghcr.io login) |
| `DOCKERHUB_USERNAME` | docker-build (DockerHub login) |
| `DOCKERHUB_TOKEN` | docker-build (DockerHub login) |
| `TENCENT_TCR_USERNAME` | docker-build (Tencent TCR login) |
| `TENCENT_TCR_PASSWORD` | docker-build (Tencent TCR login) |
| `TELEGRAM_BOT_TOKEN` | sync-upstream, docker-build (notifications) |
| `TELEGRAM_CHAT_ID` | sync-upstream, docker-build (notifications) |

## 5. sync-upstream.yml

- **Trigger:** Daily UTC 0:00 + manual `workflow_dispatch`
- **Upstream:** `https://github.com/walkingddd/CPA-Helper.git` (main)
- **Target branch:** `feat/api-key-entries`
- **Push URL:** `https://vfhky:${{ secrets.GHCR_TOKEN }}@github.com/vfhky/CPA-Helper.git`
- **On conflict:** `git merge --abort`, notify failure via Telegram
- **On success with changes:** push + Telegram notification

## 6. docker-build.yml

- **Trigger:** Push to `feat/api-key-entries` + manual `workflow_dispatch`
- **Concurrency:** cancel-in-progress on same ref
- **Image name:** `cpa-helper`
- **Platforms:** `linux/amd64`, `linux/arm64` (QEMU emulation)
- **Registries:** ghcr.io/vfhky, docker.io/vfhky, ccr.ccs.tencentyun.com/typecodes
- **Tags:** `latest`, `<short-sha>`, `<YYYYMMDD-HHMMSS>`
- **Multi-arch manifest:** `docker buildx imagetools create`
- **Notifications:** Telegram (start, success, failure)

## 7. cpah-watcher (Watchtower)

- **Image:** `containrrr/watchtower:latest`
- **Poll interval:** 300 seconds
- **Target container:** `cpah-cpah-1` (DockerHub platform cpah service)
- **Actions:** pull new image → stop old container → start new container (same config)
- **Notifications:** Telegram on update detected
- **Logging:** json-file, max 10MB × 3 files

## 8. cpah .env Update

```diff
- CPAH_IMAGE=walkingd/cpa-helper:latest
+ CPAH_IMAGE=ccr.ccs.tencentyun.com/typecodes/cpa-helper:latest
```

TCR provides faster pull speeds for Chinese servers.

## 9. Design Decisions

| Decision | Rationale |
|----------|-----------|
| Same secrets as CLIProxyAPI | No new secret provisioning needed; both repos under vfhky |
| Three registries (GHCR/DockerHub/TCR) | Redundancy + TCR for fast domestic pulls |
| No release workflow (yet) | CPA-Helper has no versioned release process; can add later when needed |
| Watchtower instead of webhook | Matches existing CLIProxyAPI pattern; no additional webhook receiver needed |
| Target container `cpah-cpah-1` | DockerHub platform naming: `<project>-<service>-1` |
