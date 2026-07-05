# CPA-Helper CI/CD Pipeline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add automated upstream sync, multi-arch Docker build/push, and Watchtower-based container auto-update for CPA-Helper.

**Architecture:** Three-stage pipeline: (1) GitHub Actions `sync-upstream.yml` merges from walkingddd/CPA-Helper main to vfhky/CPA-Helper feat/api-key-entries daily, (2) `docker-build.yml` builds and pushes multi-arch images to GHCR/DockerHub/TCR on push, (3) Watchtower container polls for new images and auto-restarts the cpah service.

**Tech Stack:** GitHub Actions, Docker Buildx, containrrr/watchtower, DockerHub Platform (initserver.platform/v1)

## Global Constraints

- Secrets are shared from CLIProxyAPI repo — do NOT create new secrets
- `IMAGE_NAME`: `cpa-helper` (not `cliproxyapi`)
- Target branch: `feat/api-key-entries`
- Upstream: `https://github.com/walkingddd/CPA-Helper.git` (main)
- Watchtower target container: `cpah-cpah-1`
- Multi-arch: amd64 + arm64
- Three registries: ghcr.io/vfhky, docker.io/vfhky, ccr.ccs.tencentyun.com/typecodes

---

### Task 1: sync-upstream.yml

**Files:**
- Create: `E:\gitHub\CPA-Helper\.github\workflows\sync-upstream.yml`

**Interfaces:**
- Consumes: GitHub secrets `GHCR_TOKEN`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`
- Produces: Upstream merge push to `feat/api-key-entries` → triggers docker-build workflow

- [ ] **Step 1: Create the workflow file**

```bash
mkdir -p "E:\gitHub\CPA-Helper\.github\workflows"
```

Write the complete file from spec §5:

```yaml
name: Sync Upstream

on:
  schedule:
    - cron: "0 0 * * *"
  workflow_dispatch:

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          ref: feat/api-key-entries
          fetch-depth: 0
          token: ${{ secrets.GHCR_TOKEN }}

      - name: Configure Git
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"

      - name: Fetch upstream
        run: |
          git remote add upstream https://github.com/walkingddd/CPA-Helper.git
          git fetch upstream main

      - name: Check for changes
        id: check
        run: |
          if git diff --quiet HEAD upstream/main; then
            echo "changed=false" >> $GITHUB_OUTPUT
          else
            echo "changed=true" >> $GITHUB_OUTPUT
          fi

      - name: Merge upstream
        id: merge_step
        if: steps.check.outputs.changed == 'true'
        run: |
          git merge upstream/main -m "chore: merge upstream/main"

      - name: Abort merge on failure
        if: failure() && steps.merge_step.outcome == 'failure'
        run: |
          git merge --abort

      - name: Push changes
        if: steps.merge_step.outcome == 'success'
        run: |
          git push https://vfhky:${{ secrets.GHCR_TOKEN }}@github.com/vfhky/CPA-Helper.git feat/api-key-entries

      - name: Notify sync failure
        if: failure()
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          format: markdown
          message: |
            ⚠️ *CPA-Helper 上游同步失败*
            📦 vfhky/CPA-Helper
            🌿 feat/api-key-entries ← upstream/main
            🔗 [查看日志](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})

      - name: Notify sync success
        if: steps.check.outputs.changed == 'true' && success()
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          format: markdown
          message: |
            🔄 *CPA-Helper 上游同步完成*
            ✅ upstream/main → feat/api-key-entries
            🚀 Docker 构建自动触发中…
            🔗 [查看日志](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})
```

- [ ] **Step 2: Commit**

```bash
cd E:\gitHub\CPA-Helper
git add .github/workflows/sync-upstream.yml
git commit -m "ci: add upstream sync workflow for CPA-Helper"
```

---

### Task 2: docker-build.yml

**Files:**
- Create: `E:\gitHub\CPA-Helper\.github\workflows\docker-build.yml`

**Interfaces:**
- Consumes: push events on `feat/api-key-entries`; GitHub secrets `GHCR_TOKEN`, `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `TENCENT_TCR_USERNAME`, `TENCENT_TCR_PASSWORD`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`
- Produces: Multi-arch Docker images at ghcr.io/vfhky/cpa-helper, docker.io/vfhky/cpa-helper, ccr.ccs.tencentyun.com/typecodes/cpa-helper

- [ ] **Step 1: Write the complete docker-build.yml**

Write exactly this content:

```yaml
name: Docker Build and Push

on:
  push:
    branches:
      - feat/api-key-entries
  workflow_dispatch:

concurrency:
  group: docker-build-${{ github.ref }}
  cancel-in-progress: true

env:
  IMAGE_NAME: cpa-helper

jobs:
  start:
    runs-on: ubuntu-latest
    steps:
      - name: Notify build started
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          format: markdown
          message: |
            🚀 *CPA-Helper Docker Build Started*
            🌿 `feat/api-key-entries`
            🏷 `${{ github.sha }}`
            📝 ${{ github.event.head_commit.message || 'manual trigger' }}
            🔗 [查看详情](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})

  build:
    runs-on: ubuntu-latest
    needs: start
    strategy:
      matrix:
        platform:
          - linux/amd64
          - linux/arm64
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Generate metadata
        id: meta
        run: |
          echo "sha=$(git rev-parse --short HEAD)" >> $GITHUB_OUTPUT
          echo "date=$(date -u +%Y%m%d-%H%M%S)" >> $GITHUB_OUTPUT

      - name: Login to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GHCR_TOKEN }}

      - name: Login to Tencent TCR
        uses: docker/login-action@v3
        with:
          registry: ccr.ccs.tencentyun.com
          username: ${{ secrets.TENCENT_TCR_USERNAME }}
          password: ${{ secrets.TENCENT_TCR_PASSWORD }}

      - name: Build and push platform image
        uses: docker/build-push-action@v6
        with:
          context: .
          platforms: ${{ matrix.platform }}
          push: true
          tags: |
            ghcr.io/vfhky/${{ env.IMAGE_NAME }}:${{ steps.meta.outputs.sha }}-${{ matrix.platform == 'linux/amd64' && 'amd64' || 'arm64' }}
            docker.io/vfhky/${{ env.IMAGE_NAME }}:${{ steps.meta.outputs.sha }}-${{ matrix.platform == 'linux/amd64' && 'amd64' || 'arm64' }}
            ccr.ccs.tencentyun.com/typecodes/${{ env.IMAGE_NAME }}:${{ steps.meta.outputs.sha }}-${{ matrix.platform == 'linux/amd64' && 'amd64' || 'arm64' }}

  manifest:
    runs-on: ubuntu-latest
    needs: build
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Generate metadata
        id: meta
        run: |
          echo "sha=$(git rev-parse --short HEAD)" >> $GITHUB_OUTPUT
          echo "date=$(date -u +%Y%m%d-%H%M%S)" >> $GITHUB_OUTPUT

      - name: Login to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GHCR_TOKEN }}

      - name: Login to Tencent TCR
        uses: docker/login-action@v3
        with:
          registry: ccr.ccs.tencentyun.com
          username: ${{ secrets.TENCENT_TCR_USERNAME }}
          password: ${{ secrets.TENCENT_TCR_PASSWORD }}

      - name: Create and push multi-arch manifests
        run: |
          SHA="${{ steps.meta.outputs.sha }}"
          DATE="${{ steps.meta.outputs.date }}"
          for REG in \
            "ghcr.io/vfhky/${IMAGE_NAME}" \
            "docker.io/vfhky/${IMAGE_NAME}" \
            "ccr.ccs.tencentyun.com/typecodes/${IMAGE_NAME}"
          do
            docker buildx imagetools create \
              --tag "${REG}:latest" \
              --tag "${REG}:${SHA}" \
              --tag "${REG}:${DATE}" \
              "${REG}:${SHA}-amd64" \
              "${REG}:${SHA}-arm64"
          done

  notify:
    runs-on: ubuntu-latest
    needs: manifest
    if: always()
    steps:
      - name: Notify build success
        if: needs.manifest.result == 'success'
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          format: markdown
          message: |
            ✅ *CPA-Helper Docker Build Success*
            🌿 `feat/api-key-entries`
            🏷 `latest` `${{ github.sha }}`
            🔗 [查看详情](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})

      - name: Notify build failure
        if: needs.manifest.result == 'failure'
        uses: appleboy/telegram-action@master
        with:
          to: ${{ secrets.TELEGRAM_CHAT_ID }}
          token: ${{ secrets.TELEGRAM_BOT_TOKEN }}
          format: markdown
          message: |
            ❌ *CPA-Helper Docker Build Failed*
            🌿 `feat/api-key-entries`
            🏷 `${{ github.sha }}`
            🔗 [查看日志](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})
```

- [ ] **Step 2: Commit**

```bash
cd E:\gitHub\CPA-Helper
git add .github/workflows/docker-build.yml
git commit -m "ci: add multi-arch docker build workflow for CPA-Helper"
```

---

### Task 3: cpah-watcher (Watchtower project)

**Files:**
- Create: `E:\gitHub\dockerHub\projects\cpah-watcher\project.yaml`
- Create: `E:\gitHub\dockerHub\projects\cpah-watcher\compose.yaml`
- Create: `E:\gitHub\dockerHub\projects\cpah-watcher\.env`

**Interfaces:**
- Consumes: Docker socket, Telegram bot credentials from .env
- Produces: Auto-updating cpah container via Watchtower

- [ ] **Step 1: Create project.yaml**

```bash
mkdir -p "E:\gitHub\dockerHub\projects\cpah-watcher"
```

Write `project.yaml`:

```yaml
apiVersion: initserver.platform/v1
kind: Project
metadata:
  name: cpah-watcher
  summary: Watchtower auto-update watcher for CPA-Helper
spec:
  kind: compose-app
  services:
    - cpah-watcher
  paths:
    envFile: .env
    envExampleFile: .env.example
    readme: README.md
    workdir: .
  runtime:
    composeFile: compose.yaml
    imageVars: []
  delivery:
    order:
      - recreate
    phases:
      recreate:
        action: up
        summary: Recreate Watchtower container
    rules:
      - name: compose
        summary: Compose definition changed
        paths:
          - compose.yaml
        require:
          - recreate
  actions:
    standard:
      - help
      - info
      - inspect
      - validate
      - status
    custom:
      - up
      - stop
      - down
      - restart
      - logs
```

- [ ] **Step 2: Create compose.yaml**

```yaml
services:
  cpah-watcher:
    image: containrrr/watchtower:latest
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - DOCKER_API_VERSION=1.41
    command: --interval 300 --cleanup --include-restarting --no-startup-message --notification-url telegram://${WATCHER_TG_BOT_TOKEN}@telegram?channels=${WATCHER_TG_CHANNEL} cpah-cpah-1
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

- [ ] **Step 3: Create .env**

Copy Telegram credentials from cliproxyapi-watcher:

```
WATCHER_TG_BOT_TOKEN=<your-telegram-bot-token>
WATCHER_TG_CHANNEL=<your-telegram-chat-id>
```

- [ ] **Step 4: Commit**

```bash
cd E:\gitHub\dockerHub
git add projects/cpah-watcher/
git commit -m "feat: add cpah-watcher for CPA-Helper auto-update"
```

---

### Task 4: Update cpah .env — image registry

**Files:**
- Modify: `E:\gitHub\dockerHub\projects\cpah\.env:2`

**Interfaces:**
- Consumes: TCR image path from docker-build workflow
- Produces: cpah service pulls from TCR instead of DockerHub

- [ ] **Step 1: Change CPAH_IMAGE**

In `E:\gitHub\dockerHub\projects\cpah\.env`, change line 2:

```diff
- CPAH_IMAGE=walkingd/cpa-helper:latest
+ CPAH_IMAGE=ccr.ccs.tencentyun.com/typecodes/cpa-helper:latest
```

- [ ] **Step 2: Commit**

```bash
cd E:\gitHub\dockerHub
git add projects/cpah/.env
git commit -m "chore: switch CPA-Helper image to Tencent TCR"
```
