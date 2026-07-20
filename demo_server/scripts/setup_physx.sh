#!/usr/bin/env bash

set -euo pipefail
shopt -s inherit_errexit 2>/dev/null || true

# 项目根目录
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
THIRD_PARTY_DIR="${ROOT_DIR}/third_party"
TOOLS_DIR="${THIRD_PARTY_DIR}/tools"
PHYSX_SRC_DIR="${THIRD_PARTY_DIR}/PhysX"
PHYSX_SDK_DIR="${THIRD_PARTY_DIR}/physx-sdk"
CMAKE_VERSION="${CMAKE_VERSION:-3.31.8}"
CMAKE_DIR="${TOOLS_DIR}/cmake-${CMAKE_VERSION}-linux-x86_64"
CMAKE_BIN="${CMAKE_DIR}/bin/cmake"
PHYSX_REPO="${PHYSX_REPO:-https://github.com/NVIDIA-Omniverse/PhysX.git}"
PHYSX_REF="${PHYSX_REF:-main}"
BUILD_TYPE="${BUILD_TYPE:-release}"
PHYSX_PRESET="${PHYSX_PRESET:-linux-gcc-cpu-only}"

mkdir -p "${THIRD_PARTY_DIR}" "${TOOLS_DIR}"

# ensure_cmake 下载用户态 CMake，避免依赖 sudo 安装系统包
ensure_cmake() {
    if command -v cmake >/dev/null 2>&1; then
        command -v cmake
        return
    fi
    if [ -x "${CMAKE_BIN}" ]; then
        echo "${CMAKE_BIN}"
        return
    fi

    local archive="${TOOLS_DIR}/cmake-${CMAKE_VERSION}-linux-x86_64.tar.gz"
    local url="https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-linux-x86_64.tar.gz"
    echo "[physx] 下载 CMake: ${url}" >&2
    curl -L --fail --retry 3 -o "${archive}" "${url}"
    tar -xzf "${archive}" -C "${TOOLS_DIR}"
    echo "${CMAKE_BIN}"
}

# ensure_physx_source 下载 PhysX 源码
ensure_physx_source() {
    if [ -d "${PHYSX_SRC_DIR}/.git" ]; then
        if [ "${PHYSX_UPDATE:-0}" = "1" ]; then
            echo "[physx] 更新 PhysX 源码: ${PHYSX_SRC_DIR}"
            git -C "${PHYSX_SRC_DIR}" fetch --depth 1 origin "${PHYSX_REF}"
            git -C "${PHYSX_SRC_DIR}" checkout FETCH_HEAD
        else
            echo "[physx] 使用已有 PhysX 源码: ${PHYSX_SRC_DIR}"
        fi
        return
    fi

    echo "[physx] 克隆 PhysX: ${PHYSX_REPO} (${PHYSX_REF})"
    rm -rf "${PHYSX_SRC_DIR}"
    git clone --depth 1 --branch "${PHYSX_REF}" "${PHYSX_REPO}" "${PHYSX_SRC_DIR}"
}

# copy_tree 复制目录内容并清理旧产物
copy_tree() {
    local src="$1"
    local dst="$2"
    rm -rf "${dst}"
    mkdir -p "${dst}"
    cp -a "${src}/." "${dst}/"
}

# copy_headers 整理 cgo 需要的头文件
copy_headers() {
    copy_tree "${PHYSX_SRC_DIR}/physx/include" "${PHYSX_SDK_DIR}/include"
    if [ -d "${PHYSX_SRC_DIR}/pxshared/include" ]; then
        copy_tree "${PHYSX_SRC_DIR}/pxshared/include" "${PHYSX_SDK_DIR}/pxshared/include"
    else
        mkdir -p "${PHYSX_SDK_DIR}/pxshared/include"
    fi
}

# copy_libraries 整理 cgo 链接需要的静态库
copy_libraries() {
    local src_lib_dir="${PHYSX_SRC_DIR}/physx/bin/linux.x86_64/${BUILD_TYPE}"
    local lib_dir="${PHYSX_SDK_DIR}/lib/linux.x86_64/release"
    rm -rf "${lib_dir}"
    mkdir -p "${lib_dir}"
    if [ ! -d "${src_lib_dir}" ]; then
        echo "[physx] 未找到 PhysX 库目录: ${src_lib_dir}" >&2
        return 1
    fi
    find "${src_lib_dir}" -maxdepth 1 -type f \( -name 'libPhysX*.a' -o -name 'libPhysX*.so' \) -print -exec cp -f {} "${lib_dir}/" \;
    if ! find "${lib_dir}" -type f -name 'libPhysXFoundation*' | grep -q .; then
        echo "[physx] 未找到 PhysX 构建产物，请检查上方构建日志" >&2
        return 1
    fi
}

# build_physx 执行 PhysX 官方 Linux 工程生成和构建
build_physx() {
    local cmake_bin="$1"
    export PATH="$(dirname "${cmake_bin}"):${PATH}"

    echo "[physx] 生成 Linux 构建工程: ${PHYSX_PRESET}"
    (
        cd "${PHYSX_SRC_DIR}/physx"
        ./generate_projects.sh "${PHYSX_PRESET}"
    )

    local compiler_dir=""
    compiler_dir="$(find "${PHYSX_SRC_DIR}/physx/compiler" -maxdepth 1 -type d -iname "*linux*${BUILD_TYPE}*" | sort | head -1 || true)"
    if [ -z "${compiler_dir}" ]; then
        compiler_dir="$(find "${PHYSX_SRC_DIR}/physx/compiler" -maxdepth 1 -type d -iname '*linux*' | sort | head -1 || true)"
    fi
    if [ -z "${compiler_dir}" ]; then
        echo "[physx] 未找到 PhysX compiler/linux 构建目录" >&2
        return 1
    fi

    echo "[physx] 构建 PhysX 核心库: ${compiler_dir}"
    if [ -f "${compiler_dir}/Makefile" ]; then
        make -C "${compiler_dir}" -j"$(nproc)" PhysX PhysXExtensions PhysXPvdSDK PhysXCommon PhysXFoundation
    else
        "${cmake_bin}" --build "${compiler_dir}" --parallel "$(nproc)" --target PhysX PhysXExtensions PhysXPvdSDK PhysXCommon PhysXFoundation
    fi
}

main() {
    local cmake_bin
    cmake_bin="$(ensure_cmake)"
    ensure_physx_source
    build_physx "${cmake_bin}"
    copy_headers
    copy_libraries
    echo "[physx] PhysX SDK 已准备完成: ${PHYSX_SDK_DIR}"
}

main "$@"
