#!/bin/bash

# filepath: /Users/tt/Desktop/work/go/plugins/search/build.sh

set -e

# 输出目录
OUTPUT_DIR="./build"
BINARY_NAME="wk.plugin.search"

# 创建输出目录
mkdir -p "$OUTPUT_DIR"

# 构建目标平台和架构
PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64")

echo "Building binaries for platforms: ${PLATFORMS[*]}"

# 遍历平台并构建
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo "$PLATFORM" | cut -d'/' -f1)
    ARCH=$(echo "$PLATFORM" | cut -d'/' -f2)
    OUTPUT_FILE="$OUTPUT_DIR/$BINARY_NAME-$OS-$ARCH.wkp"

    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH CGO_ENABLED=0 go build -o "$OUTPUT_FILE" main.go
    echo "Built: $OUTPUT_FILE"
done

echo "All binaries are built and stored in $OUTPUT_DIR"