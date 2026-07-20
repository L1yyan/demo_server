#!/usr/bin/env bash

set -euo pipefail

# 项目根目录
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
SRC_DIR="${ROOT_DIR}/src"
PHYSX_SDK_DIR="${ROOT_DIR}/third_party/physx-sdk"

# 创建可执行文件输出目录
mkdir -p "${BIN_DIR}"

services=()
while IFS= read -r cmd_dir; do
    service_dir="$(dirname "${cmd_dir}")"
    service_name="$(basename "${service_dir}")"
    services+=("${service_name}")
done < <(find "${SRC_DIR}" -mindepth 2 -maxdepth 2 -type d -path "${SRC_DIR}/*server/cmd" | sort)

if [ "${#services[@]}" -eq 0 ]; then
    echo "[build] 未找到服务入口: ${SRC_DIR}/*server/cmd" >&2
    exit 1
fi

if [ ! -d "${PHYSX_SDK_DIR}" ]; then
    echo "[build] 未找到 PhysX SDK: ${PHYSX_SDK_DIR}" >&2
    echo "[build] 请先执行 scripts/setup_physx.sh" >&2
    exit 1
fi

export CGO_ENABLED=1

echo "[build] 输出目录: ${BIN_DIR}"
echo "[build] PhysX SDK: ${PHYSX_SDK_DIR}"

for service_name in "${services[@]}"; do
    package_path="./src/${service_name}/cmd"
    output_path="${BIN_DIR}/${service_name}"
    tags=()
    if [ "${service_name}" = "roomserver" ]; then
        tags=("-tags" "physx")
    fi

    echo "[build] 编译 ${service_name}: ${package_path}"
    (
        cd "${ROOT_DIR}"
        go build -trimpath "${tags[@]}" -o "${output_path}" "${package_path}"
    )
    echo "[build] 完成 ${output_path}"
done

echo "[build] 全部服务编译完成"
