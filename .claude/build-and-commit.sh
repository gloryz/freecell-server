#!/bin/bash
set -euo pipefail

FILE=$(jq -r '.tool_input.file_path // ""')

# .go / go.mod / go.sum 파일만 처리
case "$FILE" in
  /Users/user/freecell-server/*.go|\
  /Users/user/freecell-server/go.mod|\
  /Users/user/freecell-server/go.sum)
    ;;
  *)
    exit 0
    ;;
esac

# 1. 버전 자동 증가 (patch)
cd /Users/user/freecell
NEW_VERSION=$(npm version patch --no-git-tag-version | tr -d 'v')

# 2. 서버 빌드 & 압축
cd /Users/user/freecell-server
go build -o freecell-server . || exit 1
tar -czf freecell-server.tar.gz freecell-server

# 3. 클라이언트 Electron 빌드 & 압축
cd /Users/user/freecell
npm run electron:build || exit 1
cd release
zip -r "FreeCell-${NEW_VERSION}-universal.zip" mac-universal/

# 오래된 버전 파일 정리 (현재 버전 제외)
find . -maxdepth 1 \( -name "FreeCell-*.dmg" -o -name "FreeCell-*.zip" -o -name "FreeCell-*.blockmap" \) \
  ! -name "FreeCell-${NEW_VERSION}-*" -delete

# 4. 클라이언트 커밋 & 푸시
cd /Users/user/freecell
git add -A
if ! git diff --cached --quiet; then
  FILENAME=$(basename "$FILE")
  git commit -m "update: ${FILENAME} (v${NEW_VERSION})"
  git push origin main
fi

# 5. 서버 커밋 & 푸시
cd /Users/user/freecell-server
git add -A
if ! git diff --cached --quiet; then
  FILENAME=$(basename "$FILE")
  git commit -m "update: ${FILENAME} (v${NEW_VERSION})"
  git push origin main
fi
