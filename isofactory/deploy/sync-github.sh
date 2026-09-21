#!/bin/bash
# ============================================================================
# sync-github.sh — push this IsoFactory working tree to the shared tools repo
# as the isofactory/ subdirectory (github.com:duanshuailong/tools.git), without
# disturbing sibling tools (mac-dhcp/ etc.).
#
# This local dir is a standalone git repo; the GitHub layout keeps IsoFactory
# under isofactory/. This script bridges the two: it exports the tracked files
# (git archive — no node_modules/dist/data/assets) into a fresh clone's
# isofactory/ subdir, commits, and pushes.
#
# Usage:  bash deploy/sync-github.sh "commit message"
# ============================================================================
set -euo pipefail

REPO_SSH="git@github.com:duanshuailong/tools.git"
SUBDIR="isofactory"
GIT_NAME="duanshuailong"
GIT_EMAIL="duanshuailong@infrawaves.com"
MSG="${1:-isofactory: sync $(date +%F\ %T)}"

SRC="$(cd "$(dirname "$0")/.." && pwd)"   # the IsoFactory working tree
if ! git -C "$SRC" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "错误: $SRC 不是 git 仓库（先在此目录 git init 并提交）" >&2
    exit 1
fi

# 1. Commit any local changes here first (so git archive HEAD is current).
#    Use --porcelain so UNTRACKED files count too (git diff alone misses them).
if [ -n "$(git -C "$SRC" status --porcelain)" ]; then
    echo "[sync] 本地有未提交改动，先在本地提交…"
    git -C "$SRC" add -A
    git -C "$SRC" -c user.name="$GIT_NAME" -c user.email="$GIT_EMAIL" commit -q -m "$MSG"
fi

# 2. Fresh clone of the tools repo.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "[sync] 克隆 $REPO_SSH …"
GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=accept-new" git clone -q "$REPO_SSH" "$TMP/repo"

# 3. Replace the isofactory/ subdir with our tracked files.
rm -rf "$TMP/repo/$SUBDIR"
mkdir -p "$TMP/repo/$SUBDIR"
git -C "$SRC" archive HEAD | tar -x -C "$TMP/repo/$SUBDIR"

# 4. Commit + push (no-op if nothing changed).
cd "$TMP/repo"
git add "$SUBDIR"
if git diff --cached --quiet; then
    echo "[sync] isofactory/ 无变化，无需推送。"
    exit 0
fi
git -c user.name="$GIT_NAME" -c user.email="$GIT_EMAIL" commit -q -m "$MSG

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=accept-new" git push -q origin HEAD
echo "[sync] 已推送到 $REPO_SSH ($SUBDIR/)"
