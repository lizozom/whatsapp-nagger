#!/usr/bin/env bash
set -euo pipefail

VERSION_FILE="internal/version/version.go"
VERSION=$(grep 'Version' "$VERSION_FILE" | head -1 | sed 's/.*"\(.*\)".*/\1/')
DEPLOY_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "Deploying v$VERSION..."
fly deploy --build-arg "VERSION=$VERSION" --build-arg "DEPLOY_DATE=$DEPLOY_DATE"
echo "Deployed v$VERSION at $DEPLOY_DATE"
