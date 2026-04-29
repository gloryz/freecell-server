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

# 1. 서버 빌드 & 압축
cd /Users/user/freecell-server
go build -o freecell-server . || exit 1
tar -czf freecell-server.tar.gz freecell-server

# 2. 클라이언트 빌드 & 압축
cd /Users/user/freecell
npm run build || exit 1
zip -r dist.zip dist/

# 3. 서버 커밋 & 푸시
cd /Users/user/freecell-server
git add -A
if ! git diff --cached --quiet; then
  FILENAME=$(basename "$FILE")
  git commit -m "update: ${FILENAME}"
  git push origin main
fi
