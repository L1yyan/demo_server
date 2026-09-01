cmake_minimum_required(VERSION 3.20)

project(physx_bridge LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(CMAKE_CONFIGURATION_TYPES)
    set(CMAKE_CONFIGURATION_TYPES debug checked profile release CACHE STRING "PhysX bridge build configurations" FORCE)
endif()

if(NOT WIN32)
    message(FATAL_ERROR "physx_bridge CMake build is only used for Windows")
endif()

# PhysX 预编译库使用 /MD (动态运行时 DLL)，debug 配置使用 /MDd
set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:debug>:Debug>DLL")

# 清空自定义配置的默认 flags，由 CMAKE_MSVC_RUNTIME_LIBRARY 和 target_compile_definitions 统一控制
set(CMAKE_CXX_FLAGS_DEBUG "" CACHE STRING "" FORCE)
set(CMAKE_CXX_FLAGS_CHECKED "" CACHE STRING "" FORCE)
set(CMAKE_CXX_FLAGS_PROFILE "" CACHE STRING "" FORCE)
set(CMAKE_CXX_FLAGS_RELEASE "" CACHE STRING "" FORCE)

# 为自定义配置设置优化级别（Visual Studio generator 不会自动为自定义配置添加优化选项）
add_compile_options(
    $<$<CONFIG:debug>:/Od>
    $<$<CONFIG:checked>:/O2>
    $<$<CONFIG:profile>:/O2>
    $<$<CONFIG:release>:/O2>
)

set(DEMO_SERVER_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/../../.." CACHE PATH "demo_server project root")
set(PHYSX_SDK_DIR "${DEMO_SERVER_ROOT}/third_party/physx-sdk" CACHE PATH "Prepared PhysX SDK directory")
set(PHYSX_CONFIG "checked" CACHE STRING "PhysX build config: checked, profile, release, debug")
set(PHYSX_LIB_DIR "${PHYSX_SDK_DIR}/lib/windows.x86_64/${PHYSX_CONFIG}" CACHE PATH "Windows PhysX library directory")
set(PHYSX_BIN_DIR "${PHYSX_SDK_DIR}/bin/windows.x86_64/${PHYSX_CONFIG}" CACHE PATH "Windows PhysX runtime directory")
set(PHYSX_IMPORT_LIB_DIR "${PHYSX_SDK_DIR}/lib/windows.x86_64/mingw" CACHE PATH "MinGW import library output directory")

add_library(physx_bridge SHARED physx_bridge.cc)

target_compile_definitions(physx_bridge PRIVATE PHYSX_BRIDGE_EXPORTS)

# 为各构建配置定义 PhysX 所需的预处理宏。
# PhysX 在 Windows 上要求必须且只能定义 NDEBUG 或 _DEBUG 中的一个。
# CMake 自定义配置(checked/profile/release)不会自动定义这些宏，需要手动指定。
target_compile_definitions(physx_bridge PRIVATE
    $<$<CONFIG:debug>:_DEBUG;PX_DEBUG=1;PX_CHECKED=1>
    $<$<CONFIG:checked>:NDEBUG;PX_CHECKED=1>
    $<$<CONFIG:profile>:NDEBUG;PX_PROFILE=1;PX_CHECKED=1>
    $<$<CONFIG:release>:NDEBUG>
)

target_include_directories(physx_bridge PRIVATE
    "${PHYSX_SDK_DIR}/include"
    "${PHYSX_SDK_DIR}/pxshared/include"
)

target_link_directories(physx_bridge PRIVATE "${PHYSX_LIB_DIR}")
target_link_libraries(physx_bridge PRIVATE
    PhysXExtensions_static_64
    PhysXCharacterKinematic_static_64
    PhysX_64
    PhysXPvdSDK_static_64
    PhysXCooking_64
    PhysXCommon_64
    PhysXFoundation_64
)

if(MSVC)
    target_compile_options(physx_bridge PRIVATE /utf-8 /EHsc)
    target_link_options(physx_bridge PRIVATE /DELAYLOAD:PhysX_64.dll /DELAYLOAD:PhysXCooking_64.dll /DELAYLOAD:PhysXCommon_64.dll /DELAYLOAD:PhysXFoundation_64.dll)
    target_link_libraries(physx_bridge PRIVATE delayimp ws2_32)
endif()

set_target_properties(physx_bridge PROPERTIES
    RUNTIME_OUTPUT_DIRECTORY "${DEMO_SERVER_ROOT}/bin/windows"
    ARCHIVE_OUTPUT_DIRECTORY "${DEMO_SERVER_ROOT}/bin/windows/lib"
    LIBRARY_OUTPUT_DIRECTORY "${DEMO_SERVER_ROOT}/bin/windows"
)

add_custom_command(TARGET physx_bridge POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E make_directory "${DEMO_SERVER_ROOT}/bin/windows/lib"
    COMMAND ${CMAKE_COMMAND} -E copy_if_different "$<TARGET_FILE:physx_bridge>" "${DEMO_SERVER_ROOT}/bin/windows/$<TARGET_FILE_NAME:physx_bridge>"
    COMMAND ${CMAKE_COMMAND} -E copy_if_different "$<TARGET_LINKER_FILE:physx_bridge>" "${DEMO_SERVER_ROOT}/bin/windows/lib/$<TARGET_LINKER_FILE_NAME:physx_bridge>"
)

add_custom_command(TARGET physx_bridge POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_directory "${PHYSX_BIN_DIR}" "${DEMO_SERVER_ROOT}/bin/windows"
)
