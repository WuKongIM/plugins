#!/bin/bash

# 设置变量
PLUGIN_NAME="wk.plugin.redis-sensitive-words"

# 定义目标平台
PLATFORMS=("linux/arm64" "linux/amd64" "darwin/amd64" "darwin/arm64")

# 创建输出目录
OUTPUT_DIR="build"
mkdir -p "$OUTPUT_DIR"

# 编译 Go 代码
for PLATFORM in "${PLATFORMS[@]}"; do
  # 解析平台信息
  IFS='/' read -r GOOS GOARCH <<< "$PLATFORM"

  # 设置输出文件名
  if [[ "$GOOS" == "windows" ]]; then
    OUTPUT_FILE="${OUTPUT_DIR}/${PLUGIN_NAME}-${GOOS}-${GOARCH}.exe"
  else
    OUTPUT_FILE="${OUTPUT_DIR}/${PLUGIN_NAME}-${GOOS}-${GOARCH}.wkp"
  fi

  echo "正在编译 Go 代码: $GOOS/$GOARCH..."
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "$OUTPUT_FILE" main.go

  if [ $? -eq 0 ]; then
    echo "打包完成: $OUTPUT_FILE"
  else
    echo "编译失败: $GOOS/$GOARCH"
    exit 1
  fi
done

echo "所有平台的编译已完成！"