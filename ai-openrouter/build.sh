#!/bin/bash

# 设置变量
PLUGIN_NAME="ai-openrouter"
VERSION="1.0.0"
OUTPUT_FILE="${PLUGIN_NAME}-${VERSION}.wkp"

# 编译 Go 代码
echo "正在编译 Go 代码..."
GOOS=linux GOARCH=arm64 go build -o "${OUTPUT_FILE}" main.go

echo "打包完成: ${OUTPUT_FILE}" 