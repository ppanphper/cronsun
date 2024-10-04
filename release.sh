#!/bin/sh

# 默认版本号
version="v0.4.0"
if [ "$#" -gt 0 ]; then
  version="$1"
fi

# 系统列表
goos="linux darwin"

# 检查 7zz 是否可用
if ! command -v 7zz >/dev/null 2>&1; then
  echo "Error: 7zz not found. Please install p7zip or use another compression tool."
  exit 1
fi

# 编译与打包
for os in $goos; do
  export GOOS="$os" GOARCH="amd64"
  echo "Building for $GOOS-$GOARCH..."

  # 执行构建脚本
  sh build.sh

  # 确保打包目录存在
  dist_dir="cronsun-$version"
  mkdir -p "$dist_dir"

  # 移动构建结果到打包目录
  mv dist/* "$dist_dir"

  # 压缩打包
  7zz a "cronsun-$version-$GOOS-$GOARCH.zip" "$dist_dir"

  # 清理打包目录
  rm -rf "$dist_dir"

  echo "Build for $GOOS-$GOARCH completed."
  echo
done
