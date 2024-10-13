#!/bin/bash

set -e
set -x

LATEST_VERSION=$(git describe --tags --always)
echo "Latest version: ${LATEST_VERSION}"
cd /usr/local/Homebrew/Library/Taps/homebrew/homebrew-core
ARCHIVE_URL=https://github.com/coder/aicommit/archive/refs/tags/${LATEST_VERSION}.tar.gz
ARCHIVE_SHA=$(curl -sL ${ARCHIVE_URL} | sha256sum | awk '{ print $1 }')
echo "Archive URL: ${ARCHIVE_URL}"
echo "Archive SHA: ${ARCHIVE_SHA}"
if [[ -z "${ARCHIVE_SHA}" ]]; then
    echo "Archive SHA is empty"
    exit 1
fi
brew bump-formula-pr --url=${ARCHIVE_URL} --sha256=${ARCHIVE_SHA} aicommit
