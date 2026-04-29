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

cd /Users/user/freecell-server

# 1. 빌드
go build -o freecell-server . || exit 1

# 2. 압축
tar -czf freecell-server.tar.gz freecell-server

# 3. 커밋 & 푸시
git add -A
if ! git diff --cached --quiet; then
  FILENAME=$(basename "$FILE")
  git commit -m "update: ${FILENAME}"
  git push origin main
fi
