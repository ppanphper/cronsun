#!/bin/sh

# Function to check if the previous command failed
function check_fail() {
  if [ $? -ne 0 ]; then
    echo "Build failed."
    exit 1
  fi
}

# Output directory
out="dist"
echo "Building files to ./$out"

# Create required directories
mkdir -p "$out/conf" || exit 1

# Build Go binaries in parallel for faster build times
ldflags="-s -w"

go build -ldflags "$ldflags" -o "$out/cronnode" ./bin/node/server.go || { echo "Failed to build cronnode"; exit 1; } &
go build -ldflags "$ldflags" -o "$out/cronweb" ./bin/web/server.go || { echo "Failed to build cronweb"; exit 1; } &
go build -ldflags "$ldflags" -o "$out/csctl" ./bin/csctl/cmd.go || { echo "Failed to build csctl"; exit 1; } &

# Wait for all background jobs to finish
wait
check_fail

# Copy .json.sample files to dist/conf directory
for source in $(find ./conf/files -name "*.json.sample"); do
  cp -f "$source" "$out/conf/$(basename "$source" .sample)" || { echo "Failed to copy $source"; exit 1; }
done

echo "Build success."
