#!/bin/bash

set -e
VERSION=0.6.3

pushd /tmp
wget https://github.com/coder/aicommit/archive/refs/tags/v${VERSION}.tar.gz
sha256sum v${VERSION}.tar.gz
popd
