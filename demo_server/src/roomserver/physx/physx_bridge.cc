//go:build physx

#include "physx_bridge.h"

#include <PxPhysicsAPI.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <mutex>
#include <unordered_map>
#include <vector>

using namespace physx;

struct player_actor {
    PxRigidDynamic* actor; // 玩家在 PhysX scene 中的 kinematic 胶囊体 actor
    double radius;         // 玩家胶囊体半径，移动 sweep 时用于重建碰撞形状
    double height;         // 玩家胶囊体总高度，用于业务脚底坐标和 PhysX 中心坐标互转
};

struct px_runtime {
    PxDefaultAllocator allocator;          // PhysX 默认内存分配器，创建 foundation 时使用
    PxDefaultErrorCallback error_callback; // PhysX 默认错误回调，用于接收 SDK 内部告警和错误
    PxFoundation* foundation;              // PhysX 基础对象，所有 PhysX 系统的根依赖
    PxPhysics* physics;                    // PhysX 主对象，用于创建 scene、material、mesh 等资源
    PxPvd* pvd;                            // PhysX Visual Debugger 对象，启用后发送调试数据
    PxPvdTransport* pvd_transport;         // PVD socket 传输对象，负责连接外部 PVD 工具
    bool pvd_enabled;                      // 当前全局 runtime 是否已启用 PVD
    bool extensions_initialized;           // PhysX extensions 是否已初始化，释放时需要成对关闭
    int ref_count;                         // 被多少个 px_world 引用，归零后释放全局 runtime
};

struct px_world {
    px_runtime* runtime;                                // 共享 PhysX runtime，多个房间 world 复用同一个实例
    PxDefaultCpuDispatcher* dispatcher;                 // 当前 scene 的 CPU 任务调度器
    PxScene* scene;                                     // 房间级物理场景，玩家、地图碰撞和查询都在这里执行
    PxMaterial* material;                               // 默认物理材质，创建地图碰撞体和玩家胶囊体时复用
    std::unordered_map<uint64_t, player_actor> players; // 玩家ID到 PhysX actor 的映射
    std::vector<PxRigidStatic*> static_actors;          // 地图静态碰撞 actor 列表，world 释放时统一回收
    std::vector<PxTriangleMesh*> triangle_meshes;       // cooked triangle mesh 资源列表，mesh actor 释放后也要回收
};

namespace {

std::mutex g_runtime_mutex;
px_runtime* g_runtime = nullptr;
constexpr double k_player_jump_speed = 5.0;
constexpr double k_player_gravity = -9.8;
constexpr PxReal k_sweep_skin_width = 0.01f;
constexpr PxReal k_ground_probe_distance = 0.12f;
constexpr PxReal k_walkable_normal_y = 0.5f;
constexpr PxReal k_ceiling_normal_y = -0.5f;

void set_error(char* err, int err_len, const char* message) {
    if (err == nullptr || err_len <= 0) {
        return;
    }
    std::strncpy(err, message, static_cast<size_t>(err_len - 1));
    err[err_len - 1] = '\0';
}

PxVec3 to_px_vec3(px_vec3 value) {
    return PxVec3(static_cast<PxReal>(value.x), static_cast<PxReal>(value.y), static_cast<PxReal>(value.z));
}

PxQuat to_px_quat(px_quat value) {
    return PxQuat(static_cast<PxReal>(value.x), static_cast<PxReal>(value.y), static_cast<PxReal>(value.z), static_cast<PxReal>(value.w));
}

px_vec3 from_px_vec3(const PxVec3& value) {
    return px_vec3{static_cast<double>(value.x), static_cast<double>(value.y), static_cast<double>(value.z)};
}

bool valid_vec3(px_vec3 value) {
    return std::isfinite(value.x) && std::isfinite(value.y) && std::isfinite(value.z);
}

bool valid_quat(px_quat value) {
    if (!std::isfinite(value.x) || !std::isfinite(value.y) || !std::isfinite(value.z) || !std::isfinite(value.w)) {
        return false;
    }
    double length_squared = value.x * value.x + value.y * value.y + value.z * value.z + value.w * value.w;
    return length_squared > 0.000001;
}

bool valid_box_size(px_vec3 value) {
    return valid_vec3(value) && value.x > 0 && value.y > 0 && value.z > 0;
}

bool valid_mesh_input(const double* vertices, int vertex_count, const int* triangles, int triangle_count) {
    if (vertices == nullptr || triangles == nullptr || vertex_count < 3 || triangle_count < 1) {
        return false;
    }
    for (int i = 0; i < vertex_count * 3; ++i) {
        if (!std::isfinite(vertices[i])) {
            return false;
        }
    }
    for (int i = 0; i < triangle_count * 3; ++i) {
        if (triangles[i] < 0 || triangles[i] >= vertex_count) {
            return false;
        }
    }
    return true;
}

class IgnoreActorFilter : public PxQueryFilterCallback {
public:
    // ignored_actor 表示本次查询需要排除的 actor，常用于移动和开火时忽略玩家自己
    explicit IgnoreActorFilter(const PxRigidActor* ignored_actor) : ignored_actor_(ignored_actor) {}

    PxQueryHitType::Enum preFilter(const PxFilterData&, const PxShape*, const PxRigidActor* actor, PxHitFlags&) override {
        // 查询命中待忽略 actor 时丢弃该命中，其他 actor 都作为阻挡处理
        if (ignored_actor_ != nullptr && actor == ignored_actor_) {
            return PxQueryHitType::eNONE;
        }
        return PxQueryHitType::eBLOCK;
    }

    PxQueryHitType::Enum postFilter(const PxFilterData&, const PxQueryHit&, const PxShape*, const PxRigidActor*) override {
        return PxQueryHitType::eNONE;
    }

private:
    const PxRigidActor* ignored_actor_; // 本次 scene query 要忽略的 actor 指针
};

PxReal capsule_half_height(double radius, double height) {
    // PhysX capsule 的 halfHeight 是中间圆柱段的一半，不是胶囊体总高度的一半
    return static_cast<PxReal>(std::max(0.01, (height - radius * 2.0) * 0.5));
}

PxTransform player_transform(px_vec3 position, double radius, double height) {
    // 业务层记录玩家脚底位置，PhysX actor 使用胶囊体中心位置
    PxReal center_y = static_cast<PxReal>(position.y + height * 0.5);
    // PhysX capsule 默认沿 X 轴，这里旋转到服务端使用的 Y 轴竖直方向
    return PxTransform(PxVec3(static_cast<PxReal>(position.x), center_y, static_cast<PxReal>(position.z)), PxQuat(PxHalfPi, PxVec3(0, 0, 1)));
}

px_vec3 actor_player_position(PxRigidDynamic* actor, double height) {
    PxTransform pose = actor->getGlobalPose();
    // 返回给 Go 侧时还原为业务脚底坐标
    return px_vec3{static_cast<double>(pose.p.x), static_cast<double>(pose.p.y) - height * 0.5, static_cast<double>(pose.p.z)};
}

void cleanup_runtime(px_runtime* runtime) {
    if (runtime == nullptr) {
        return;
    }
    if (runtime->extensions_initialized) {
        PxCloseExtensions();
        runtime->extensions_initialized = false;
    }
    if (runtime->physics != nullptr) {
        runtime->physics->release();
        runtime->physics = nullptr;
    }
    if (runtime->pvd != nullptr) {
        runtime->pvd->disconnect();
        runtime->pvd->release();
        runtime->pvd = nullptr;
    }
    if (runtime->pvd_transport != nullptr) {
        runtime->pvd_transport->release();
        runtime->pvd_transport = nullptr;
    }
    if (runtime->foundation != nullptr) {
        runtime->foundation->release();
        runtime->foundation = nullptr;
    }
}

bool init_pvd(px_runtime* runtime, px_pvd_config config, char* err, int err_len) {
    if (config.enabled == 0) {
        return true;
    }
    if (config.host == nullptr || config.host[0] == '\0' || config.port <= 0 || config.timeout_ms == 0) {
        set_error(err, err_len, "invalid pvd config");
        return false;
    }

    runtime->pvd = PxCreatePvd(*runtime->foundation);
    if (runtime->pvd == nullptr) {
        set_error(err, err_len, "create pvd failed");
        return false;
    }
    runtime->pvd_transport = PxDefaultPvdSocketTransportCreate(config.host, config.port, config.timeout_ms);
    if (runtime->pvd_transport == nullptr) {
        set_error(err, err_len, "create pvd transport failed");
        return false;
    }
    if (!runtime->pvd->connect(*runtime->pvd_transport, PxPvdInstrumentationFlag::eALL)) {
        char message[256];
        std::snprintf(message, sizeof(message), "connect pvd failed: %s:%d", config.host, config.port);
        set_error(err, err_len, message);
        return false;
    }
    runtime->pvd_enabled = true;
    return true;
}

bool configure_scene_pvd(PxScene* scene, char* err, int err_len) {
    if (scene == nullptr) {
        set_error(err, err_len, "scene is nil");
        return false;
    }
    PxPvdSceneClient* pvd_client = scene->getScenePvdClient();
    if (pvd_client == nullptr) {
        set_error(err, err_len, "pvd scene client unavailable");
        return false;
    }

    pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_CONTACTS, true);
    pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_SCENEQUERIES, true);
    pvd_client->setScenePvdFlag(PxPvdSceneFlag::eTRANSMIT_CONSTRAINTS, true);
    return true;
}

px_runtime* acquire_runtime(px_pvd_config pvd_config, char* err, int err_len) {
    std::lock_guard<std::mutex> lock(g_runtime_mutex);
    if (g_runtime != nullptr) {
        // 全局 runtime 已创建时直接复用，避免每个房间重复初始化 PhysX SDK
        if (pvd_config.enabled != 0 && !g_runtime->pvd_enabled) {
            set_error(err, err_len, "pvd must be enabled before physx runtime is created");
            return nullptr;
        }
        ++g_runtime->ref_count;
        return g_runtime;
    }

    px_runtime* runtime = new px_runtime{};
    // Foundation 是 PhysX 根对象，后续 Physics、PVD、extensions 都依赖它
    runtime->foundation = PxCreateFoundation(PX_PHYSICS_VERSION, runtime->allocator, runtime->error_callback);
    if (runtime->foundation == nullptr) {
        set_error(err, err_len, "create foundation failed");
        delete runtime;
        return nullptr;
    }

    // PVD 必须在 PxPhysics 创建前挂到 runtime 上，后续 scene 才能发送调试数据
    if (!init_pvd(runtime, pvd_config, err, err_len)) {
        cleanup_runtime(runtime);
        delete runtime;
        return nullptr;
    }

    // PxPhysics 是创建 scene、material、mesh 的主入口
    runtime->physics = PxCreatePhysics(PX_PHYSICS_VERSION, *runtime->foundation, PxTolerancesScale(), true, runtime->pvd);
    if (runtime->physics == nullptr) {
        set_error(err, err_len, "create physics failed");
        cleanup_runtime(runtime);
        delete runtime;
        return nullptr;
    }
    // extensions 初始化成功后必须在 runtime 清理时调用 PxCloseExtensions
    if (!PxInitExtensions(*runtime->physics, runtime->pvd)) {
        set_error(err, err_len, "init physx extensions failed");
        cleanup_runtime(runtime);
        delete runtime;
        return nullptr;
    }
    runtime->extensions_initialized = true;

    runtime->ref_count = 1;
    g_runtime = runtime;
    return runtime;
}

void release_runtime(px_runtime* runtime) {
    if (runtime == nullptr) {
        return;
    }

    std::lock_guard<std::mutex> lock(g_runtime_mutex);
    if (runtime != g_runtime || g_runtime->ref_count <= 0) {
        return;
    }

    --g_runtime->ref_count;
    if (g_runtime->ref_count > 0) {
        return;
    }

    px_runtime* released = g_runtime;
    g_runtime = nullptr;
    cleanup_runtime(released);
    delete released;
}

PxPhysics* world_physics(px_world* world) {
    if (world == nullptr || world->runtime == nullptr) {
        return nullptr;
    }
    return world->runtime->physics;
}

} // namespace

extern "C" {

px_world* px_world_create(int create_ground_plane, px_pvd_config pvd_config, char* err, int err_len) {
    px_runtime* runtime = acquire_runtime(pvd_config, err, err_len);
    if (runtime == nullptr) {
        return nullptr;
    }

    px_world* world = new px_world{};
    world->runtime = runtime;

    world->dispatcher = PxDefaultCpuDispatcherCreate(1);
    if (world->dispatcher == nullptr) {
        set_error(err, err_len, "create dispatcher failed");
        px_world_release(world);
        return nullptr;
    }

    PxSceneDesc scene_desc(runtime->physics->getTolerancesScale());
    scene_desc.gravity = PxVec3(0.0f, -9.81f, 0.0f);
    scene_desc.cpuDispatcher = world->dispatcher;
    scene_desc.filterShader = PxDefaultSimulationFilterShader;
    world->scene = runtime->physics->createScene(scene_desc);
    if (world->scene == nullptr) {
        set_error(err, err_len, "create scene failed");
        px_world_release(world);
        return nullptr;
    }
    if (runtime->pvd_enabled && !configure_scene_pvd(world->scene, err, err_len)) {
        px_world_release(world);
        return nullptr;
    }

    world->material = runtime->physics->createMaterial(0.5f, 0.5f, 0.6f);
    if (world->material == nullptr) {
        set_error(err, err_len, "create material failed");
        px_world_release(world);
        return nullptr;
    }

    if (create_ground_plane != 0) {
        PxRigidStatic* plane = PxCreatePlane(*runtime->physics, PxPlane(0, 1, 0, 0), *world->material);
        if (plane == nullptr) {
            set_error(err, err_len, "create ground plane failed");
            px_world_release(world);
            return nullptr;
        }
        world->scene->addActor(*plane);
        plane->release();
    }

    return world;
}

void px_world_release(px_world* world) {
    if (world == nullptr) {
        return;
    }
    for (auto& item : world->players) {
        if (item.second.actor != nullptr) {
            item.second.actor->release();
        }
    }
    world->players.clear();
    for (auto* actor : world->static_actors) {
        if (actor != nullptr) {
            actor->release();
        }
    }
    world->static_actors.clear();
    for (auto* mesh : world->triangle_meshes) {
        if (mesh != nullptr) {
            mesh->release();
        }
    }
    world->triangle_meshes.clear();
    if (world->material != nullptr) {
        world->material->release();
    }
    if (world->scene != nullptr) {
        world->scene->release();
    }
    if (world->dispatcher != nullptr) {
        world->dispatcher->release();
    }
    release_runtime(world->runtime);
    world->runtime = nullptr;
    delete world;
}

int px_world_add_static_box(px_world* world, px_vec3 position, px_quat rotation, px_vec3 size, char* err, int err_len) {
    PxPhysics* physics = world_physics(world);
    if (physics == nullptr || world->scene == nullptr || world->material == nullptr) {
        set_error(err, err_len, "world is nil");
        return 1;
    }
    if (!valid_vec3(position) || !valid_quat(rotation) || !valid_box_size(size)) {
        set_error(err, err_len, "invalid static box");
        return 1;
    }

    PxBoxGeometry geometry(static_cast<PxReal>(size.x * 0.5), static_cast<PxReal>(size.y * 0.5), static_cast<PxReal>(size.z * 0.5));
    if (!geometry.isValid()) {
        set_error(err, err_len, "invalid static box geometry");
        return 1;
    }

    PxQuat quat = to_px_quat(rotation).getNormalized();
    PxTransform transform(to_px_vec3(position), quat);
    if (!transform.isValid()) {
        set_error(err, err_len, "invalid static box transform");
        return 1;
    }

    PxRigidStatic* actor = PxCreateStatic(*physics, transform, geometry, *world->material);
    if (actor == nullptr) {
        set_error(err, err_len, "create static box failed");
        return 1;
    }
    world->scene->addActor(*actor);
    world->static_actors.push_back(actor);
    return 0;
}

int px_world_add_static_mesh(px_world* world, const double* vertices, int vertex_count, const int* triangles, int triangle_count, char* err, int err_len) {
    PxPhysics* physics = world_physics(world);
    if (physics == nullptr || world->scene == nullptr || world->material == nullptr) {
        set_error(err, err_len, "world is nil");
        return 1;
    }
    if (!valid_mesh_input(vertices, vertex_count, triangles, triangle_count)) {
        set_error(err, err_len, "invalid static mesh");
        return 1;
    }

    std::vector<PxVec3> px_vertices;
    px_vertices.reserve(static_cast<size_t>(vertex_count));
    for (int i = 0; i < vertex_count; ++i) {
        int offset = i * 3;
        px_vertices.emplace_back(static_cast<PxReal>(vertices[offset]), static_cast<PxReal>(vertices[offset + 1]), static_cast<PxReal>(vertices[offset + 2]));
    }

    std::vector<PxU32> px_triangles;
    px_triangles.reserve(static_cast<size_t>(triangle_count * 3));
    for (int i = 0; i < triangle_count * 3; ++i) {
        px_triangles.push_back(static_cast<PxU32>(triangles[i]));
    }

    PxTriangleMeshDesc mesh_desc;
    mesh_desc.points.count = static_cast<PxU32>(vertex_count);
    mesh_desc.points.stride = sizeof(PxVec3);
    mesh_desc.points.data = px_vertices.data();
    mesh_desc.triangles.count = static_cast<PxU32>(triangle_count);
    mesh_desc.triangles.stride = sizeof(PxU32) * 3;
    mesh_desc.triangles.data = px_triangles.data();
    if (!mesh_desc.isValid()) {
        set_error(err, err_len, "invalid static mesh desc");
        return 1;
    }

    PxCookingParams cooking_params(physics->getTolerancesScale());
    PxTriangleMeshCookingResult::Enum cooking_result;
    PxTriangleMesh* triangle_mesh = PxCreateTriangleMesh(cooking_params, mesh_desc, physics->getPhysicsInsertionCallback(), &cooking_result);
    if (triangle_mesh == nullptr) {
        set_error(err, err_len, "create triangle mesh failed");
        return 1;
    }

    PxTriangleMeshGeometry geometry(triangle_mesh, PxMeshScale(), PxMeshGeometryFlag::eDOUBLE_SIDED);
    if (!geometry.isValid()) {
        triangle_mesh->release();
        set_error(err, err_len, "invalid triangle mesh geometry");
        return 1;
    }

    PxRigidStatic* actor = PxCreateStatic(*physics, PxTransform(PxIdentity), geometry, *world->material);
    if (actor == nullptr) {
        triangle_mesh->release();
        set_error(err, err_len, "create static mesh actor failed");
        return 1;
    }
    world->scene->addActor(*actor);
    world->static_actors.push_back(actor);
    world->triangle_meshes.push_back(triangle_mesh);
    return 0;
}

int px_world_add_player_capsule(px_world* world, uint64_t player_id, px_vec3 position, double radius, double height, char* err, int err_len) {
    PxPhysics* physics = world_physics(world);
    if (physics == nullptr || world->scene == nullptr || world->material == nullptr) {
        set_error(err, err_len, "world is nil");
        return 1;
    }
    if (player_id == 0 || radius <= 0 || height <= radius * 2.0 || !valid_vec3(position)) {
        set_error(err, err_len, "invalid player capsule");
        return 1;
    }
    if (world->players.find(player_id) != world->players.end()) {
        set_error(err, err_len, "player already exists");
        return 1;
    }

    PxCapsuleGeometry geometry(static_cast<PxReal>(radius), capsule_half_height(radius, height));
    PxRigidDynamic* actor = PxCreateDynamic(*physics, player_transform(position, radius, height), geometry, *world->material, 1.0f);
    if (actor == nullptr) {
        set_error(err, err_len, "create player actor failed");
        return 1;
    }
    actor->userData = reinterpret_cast<void*>(static_cast<uintptr_t>(player_id));
    actor->setRigidBodyFlag(PxRigidBodyFlag::eKINEMATIC, true);
    actor->setActorFlag(PxActorFlag::eDISABLE_GRAVITY, true);
    world->scene->addActor(*actor);
    world->players[player_id] = player_actor{actor, radius, height};
    return 0;
}

int px_world_remove_player(px_world* world, uint64_t player_id, char* err, int err_len) {
    if (world == nullptr) {
        set_error(err, err_len, "world is nil");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end()) {
        return 0;
    }
    if (iter->second.actor != nullptr) {
        iter->second.actor->release();
    }
    world->players.erase(iter);
    return 0;
}

int px_world_move_player(px_world* world, uint64_t player_id, px_vec3 direction, double distance, double delta_time, int jump, int grounded, double vertical_velocity, px_vec3* out_position, int* out_blocked, int* out_grounded, double* out_vertical_velocity, char* err, int err_len) {
    if (world == nullptr || out_position == nullptr || out_blocked == nullptr || out_grounded == nullptr || out_vertical_velocity == nullptr) {
        set_error(err, err_len, "invalid move request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.actor == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    if (!valid_vec3(direction) || !std::isfinite(distance) || distance < 0 || !std::isfinite(delta_time) || delta_time <= 0 || !std::isfinite(vertical_velocity)) {
        set_error(err, err_len, "invalid move value");
        return 1;
    }

    PxRigidDynamic* actor = iter->second.actor;
    PxVec3 horizontal = to_px_vec3(direction);
    PxReal horizontal_length = horizontal.magnitude();
    if (horizontal_length > 0.0001f && distance > 0) {
        horizontal = horizontal / horizontal_length * static_cast<PxReal>(distance);
    } else {
        horizontal = PxVec3(0.0f);
    }

    double next_vertical_velocity = vertical_velocity;
    if (jump != 0 && grounded != 0) {
        next_vertical_velocity = k_player_jump_speed + k_player_gravity * delta_time;
    } else if (grounded != 0) {
        next_vertical_velocity = 0.0;
    } else {
        next_vertical_velocity += k_player_gravity * delta_time;
    }
    PxVec3 displacement = horizontal + PxVec3(0.0f, static_cast<PxReal>(next_vertical_velocity * delta_time), 0.0f);

    PxCapsuleGeometry geometry(static_cast<PxReal>(iter->second.radius), capsule_half_height(iter->second.radius, iter->second.height));
    PxTransform current = actor->getGlobalPose();
    PxQueryFilterData filter_data(PxQueryFlag::eSTATIC | PxQueryFlag::eDYNAMIC | PxQueryFlag::ePREFILTER);
    IgnoreActorFilter filter_callback(actor);

    bool blocked = false;
    bool is_grounded = false;
    PxTransform next = current;
    PxReal move_distance = displacement.magnitude();
    if (move_distance > 0.0001f) {
        PxVec3 move_dir = displacement / move_distance;
        PxSweepBuffer sweep_hit;
        blocked = world->scene->sweep(geometry, current, move_dir, move_distance, sweep_hit, PxHitFlag::eDEFAULT, filter_data, &filter_callback);
        PxReal travel = move_distance;
        if (blocked && sweep_hit.hasBlock) {
            travel = std::max<PxReal>(0.0f, sweep_hit.block.distance - k_sweep_skin_width);
            if (sweep_hit.block.normal.y >= k_walkable_normal_y && next_vertical_velocity <= 0.0) {
                is_grounded = true;
                next_vertical_velocity = 0.0;
            } else if (sweep_hit.block.normal.y <= k_ceiling_normal_y && next_vertical_velocity > 0.0) {
                next_vertical_velocity = 0.0;
            }
        }
        next.p += move_dir * travel;
        
    }

    PxSweepBuffer ground_hit;
    bool ground_blocked = world->scene->sweep(geometry, next, PxVec3(0.0f, -1.0f, 0.0f), k_ground_probe_distance, ground_hit, PxHitFlag::eDEFAULT, filter_data, &filter_callback);
    if (ground_blocked && ground_hit.hasBlock && ground_hit.block.normal.y >= k_walkable_normal_y && next_vertical_velocity <= 0.0) {
        is_grounded = true;
        next_vertical_velocity = 0.0;
    }

    actor->setKinematicTarget(next);
    world->scene->simulate(static_cast<PxReal>(delta_time));
    world->scene->fetchResults(true);
    actor->setGlobalPose(next);

    *out_position = actor_player_position(actor, iter->second.height);
    *out_blocked = blocked ? 1 : 0;
    *out_grounded = is_grounded ? 1 : 0;
    *out_vertical_velocity = next_vertical_velocity;
    return 0;
}

int px_world_get_player_position(px_world* world, uint64_t player_id, px_vec3* out_position, char* err, int err_len) {
    if (world == nullptr || out_position == nullptr) {
        set_error(err, err_len, "invalid get position request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.actor == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    *out_position = actor_player_position(iter->second.actor, iter->second.height);
    return 0;
}

int px_world_set_player_position(px_world* world, uint64_t player_id, px_vec3 position, char* err, int err_len) {
    if (world == nullptr || !valid_vec3(position)) {
        set_error(err, err_len, "invalid set position request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.actor == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    PxTransform next = player_transform(position, iter->second.radius, iter->second.height);
    iter->second.actor->setKinematicTarget(next);
    iter->second.actor->setGlobalPose(next);
    return 0;
}

int px_world_raycast(px_world* world, px_vec3 origin, px_vec3 direction, double max_distance, uint32_t mask, uint64_t ignored_player_id, px_raycast_hit* out_hit, char* err, int err_len) {
    if (world == nullptr || out_hit == nullptr) {
        set_error(err, err_len, "invalid raycast request");
        return 1;
    }
    if (!valid_vec3(origin) || !valid_vec3(direction) || !std::isfinite(max_distance) || max_distance <= 0) {
        set_error(err, err_len, "invalid raycast value");
        return 1;
    }

    PxVec3 dir = to_px_vec3(direction);
    PxReal length = dir.magnitude();
    if (length <= 0.0001f) {
        set_error(err, err_len, "zero raycast direction");
        return 1;
    }
    dir /= length;

    PxRigidActor* ignored_actor = nullptr;
    if (ignored_player_id != 0) {
        auto ignored_iter = world->players.find(ignored_player_id);
        if (ignored_iter != world->players.end()) {
            ignored_actor = ignored_iter->second.actor;
        }
    }

    PxRaycastBuffer hit;
    PxQueryFilterData filter_data(PxQueryFlag::eSTATIC | PxQueryFlag::eDYNAMIC | PxQueryFlag::ePREFILTER);
    IgnoreActorFilter filter_callback(ignored_actor);
    bool has_hit = world->scene->raycast(to_px_vec3(origin), dir, static_cast<PxReal>(max_distance), hit, PxHitFlag::eDEFAULT, filter_data, &filter_callback);
    *out_hit = px_raycast_hit{};
    
    if (!has_hit || !hit.hasBlock) {
        return 0;
    }

    out_hit->hit = 1;
    out_hit->distance = static_cast<double>(hit.block.distance);
    out_hit->point = from_px_vec3(hit.block.position);
    out_hit->normal = from_px_vec3(hit.block.normal);
    if (hit.block.actor != nullptr) {
        out_hit->target_id = static_cast<uint64_t>(reinterpret_cast<uintptr_t>(hit.block.actor->userData));
    }
    return 0;
}

int px_world_batch_raycast(px_world* world, const px_vec3* origins, const px_vec3* directions, const double* max_distances, const uint32_t* masks, const uint64_t* ignored_player_ids, int count, px_raycast_hit* out_hits, char* err, int err_len) {
    if (world == nullptr || origins == nullptr || directions == nullptr || max_distances == nullptr || masks == nullptr || ignored_player_ids == nullptr || out_hits == nullptr || count < 0) {
        set_error(err, err_len, "invalid batch raycast request");
        return 1;
    }
    for (int i = 0; i < count; ++i) {
        int code = px_world_raycast(world, origins[i], directions[i], max_distances[i], masks[i], ignored_player_ids[i], &out_hits[i], err, err_len);
        if (code != 0) {
            return code;
        }
    }
    return 0;
}

} // extern "C"
