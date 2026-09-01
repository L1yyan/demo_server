//go:build physx && !windows

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
    PxCapsuleController* controller; // 玩家使用的 PhysX 胶囊体控制器
    PxRigidDynamic* actor;            // CCT 内部代理 actor，供射线查询使用
    double radius;                    // 玩家胶囊体半径
    double height;                    // 玩家站立时胶囊体总高度
    double crouch_height;             // 玩家下蹲时胶囊体总高度
    double contact_offset;            // CCT 接触偏移，用于保持业务脚底坐标稳定
    bool crouched;                    // 当前是否处于下蹲状态
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
    PxControllerManager* controller_manager;            // 房间级 CCT 管理器，每个 scene 只能创建一个
    std::unordered_map<uint64_t, player_actor> players; // 玩家ID到 CCT 和代理 actor 的映射
    std::vector<PxRigidStatic*> static_actors;          // 地图静态碰撞 actor 列表，world 释放时统一回收
    std::vector<PxTriangleMesh*> triangle_meshes;       // cooked triangle mesh 资源列表，mesh actor 释放后也要回收
};

namespace {

std::mutex g_runtime_mutex;
px_runtime* g_runtime = nullptr;
constexpr double k_player_jump_speed = 5.0;
constexpr double k_player_gravity = -9.8;
constexpr PxReal k_cct_contact_offset = 0.01f;
constexpr PxReal k_cct_step_offset = 0.5f;
constexpr PxReal k_cct_slope_limit = 0.0f;
constexpr double k_player_crouch_height = 1.0;

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

class IgnoreCCTFilter : public PxControllerFilterCallback {
public:
    // 当前项目不新增玩家之间的阻挡或推动，显式过滤所有 CCT 对之间的交互
    bool filter(const PxController&, const PxController&) override {
        return false;
    }
};

bool has_overlap(PxScene* scene, const PxGeometry& geometry, const PxTransform& pose, const PxRigidActor* ignored_actor) {
    if (scene == nullptr) {
        return true;
    }
    PxQueryFilterData filter_data(
        PxQueryFlag::eSTATIC |
        PxQueryFlag::eDYNAMIC |
        PxQueryFlag::ePREFILTER |
        PxQueryFlag::eANY_HIT
    );
    IgnoreActorFilter filter_callback(ignored_actor);
    PxOverlapBuffer overlap;
    return scene->overlap(geometry, pose, overlap, filter_data, &filter_callback);
}

PxReal capsule_controller_height(double radius, double height) {
    // CCT height 表示两端球心距离，业务 height 表示胶囊体端到端总高度
    return static_cast<PxReal>(height - radius * 2.0);
}

PxExtendedVec3 controller_center_position(px_vec3 position, double height, double contact_offset) {
    // 业务层使用胶囊底部坐标，CCT 中心还要加上半高和接触偏移
    return PxExtendedVec3(static_cast<PxExtended>(position.x), static_cast<PxExtended>(position.y + height * 0.5 + contact_offset), static_cast<PxExtended>(position.z));
}

PxExtendedVec3 controller_foot_position(px_vec3 position) {
    // setFootPosition 接受包含 contact offset 语义的业务脚底坐标
    return PxExtendedVec3(static_cast<PxExtended>(position.x), static_cast<PxExtended>(position.y), static_cast<PxExtended>(position.z));
}

void refresh_scene_queries(PxScene* scene) {
    if (scene == nullptr) {
        return;
    }
    scene->flushQueryUpdates();
    scene->sceneQueriesUpdate();
    scene->fetchQueries(true);
}

px_vec3 controller_player_position(PxCapsuleController* controller) {
    PxExtendedVec3 foot = controller->getFootPosition();
    // getFootPosition 的坐标契约与业务脚底坐标保持一致，contact offset 已包含在 CCT foot 定义中
    return px_vec3{static_cast<double>(foot.x), static_cast<double>(foot.y), static_cast<double>(foot.z)};
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

    // 开启 PhysX 调试渲染总开关和碰撞形状，确保 PVD 生成可视化几何
    if (!scene->setVisualizationParameter(PxVisualizationParameter::eSCALE, 1.0f)) {
        set_error(err, err_len, "set pvd visualization scale failed");
        return false;
    }
    if (!scene->setVisualizationParameter(PxVisualizationParameter::eCOLLISION_SHAPES, 1.0f)) {
        set_error(err, err_len, "set pvd collision shapes visualization failed");
        return false;
    }
    if (!scene->setVisualizationParameter(PxVisualizationParameter::eCOLLISION_AXES, 1.0f)) {
        set_error(err, err_len, "set pvd collision axes visualization failed");
        return false;
    }
    if (!scene->setVisualizationParameter(PxVisualizationParameter::eACTOR_AXES, 1.0f)) {
        set_error(err, err_len, "set pvd actor axes visualization failed");
        return false;
    }

    // 开启接触、场景查询和约束数据传输，便于在 PVD 中观察物理调试过程
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

PHYSX_BRIDGE_API px_world* px_world_create(int create_ground_plane, px_pvd_config pvd_config, char* err, int err_len) {
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

    // 每个 PhysX scene 只创建一个 CCT manager，后续房间内玩家 controller 由它统一管理
    world->controller_manager = PxCreateControllerManager(*world->scene);
    if (world->controller_manager == nullptr) {
        set_error(err, err_len, "create controller manager failed");
        px_world_release(world);
        return nullptr;
    }
    world->controller_manager->setOverlapRecoveryModule(true);

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

PHYSX_BRIDGE_API void px_world_release(px_world* world) {
    if (world == nullptr) {
        return;
    }

    // CCT manager 会持有 controller 和底层 proxy actor，必须先于 scene、dispatcher 和 runtime 释放
    if (world->controller_manager != nullptr) {
        world->controller_manager->release();
        world->controller_manager = nullptr;
    }

    // manager.release 已经释放全部 controller 及其底层 proxy actor，不能再次释放 actor
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

PHYSX_BRIDGE_API int px_world_add_static_box(px_world* world, px_vec3 position, px_quat rotation, px_vec3 size, char* err, int err_len) {
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

PHYSX_BRIDGE_API int px_world_add_static_mesh(px_world* world, const double* vertices, int vertex_count, const int* triangles, int triangle_count, char* err, int err_len) {
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

PHYSX_BRIDGE_API int px_world_add_player_capsule(px_world* world, uint64_t player_id, px_vec3 position, double radius, double height, char* err, int err_len) {
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

    if (world->controller_manager == nullptr) {
        set_error(err, err_len, "controller manager is nil");
        return 1;
    }

    const PxReal controller_radius = static_cast<PxReal>(radius);
    const PxReal controller_height = capsule_controller_height(radius, height);
    const PxReal crouch_height = capsule_controller_height(radius, k_player_crouch_height);
    if (controller_height <= 0.0f || crouch_height <= 0.0f) {
        set_error(err, err_len, "invalid player controller height");
        return 1;
    }

    // CCT 的 position 是碰撞体中心，先按业务脚底坐标设置初始中心位置
    PxCapsuleControllerDesc desc;
    desc.position = controller_center_position(position, height, k_cct_contact_offset);
    desc.upDirection = PxVec3(0.0f, 1.0f, 0.0f);
    desc.radius = controller_radius;
    desc.height = controller_height;
    desc.contactOffset = k_cct_contact_offset;
    desc.stepOffset = std::min(k_cct_step_offset, static_cast<PxReal>(height));
    desc.slopeLimit = k_cct_slope_limit;
    desc.material = world->material;
    desc.userData = reinterpret_cast<void*>(static_cast<uintptr_t>(player_id));
    if (!desc.isValid()) {
        set_error(err, err_len, "invalid player controller desc");
        return 1;
    }

    PxController* base_controller = world->controller_manager->createController(desc);
    if (base_controller == nullptr) {
        set_error(err, err_len, "create player controller failed");
        return 1;
    }
    PxCapsuleController* controller = static_cast<PxCapsuleController*>(base_controller);
    PxRigidDynamic* actor = controller->getActor();
    if (actor == nullptr) {
        controller->release();
        set_error(err, err_len, "create player controller actor failed");
        return 1;
    }
    // userData 需要同时设置在 controller 和其内部代理 actor 上，保证射线可以返回玩家 ID
    controller->setUserData(reinterpret_cast<void*>(static_cast<uintptr_t>(player_id)));
    actor->userData = reinterpret_cast<void*>(static_cast<uintptr_t>(player_id));
    world->players[player_id] = player_actor{controller, actor, radius, height, k_player_crouch_height, static_cast<double>(k_cct_contact_offset), false};
    return 0;
}

PHYSX_BRIDGE_API int px_world_remove_player(px_world* world, uint64_t player_id, char* err, int err_len) {
    if (world == nullptr) {
        set_error(err, err_len, "world is nil");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end()) {
        return 0;
    }
    // controller 的底层代理 actor 由 controller manager 管理，只释放 controller
    if (iter->second.controller != nullptr) {
        iter->second.controller->release();
    }
    world->players.erase(iter);
    return 0;
}

PHYSX_BRIDGE_API int px_world_move_player(px_world* world, uint64_t player_id, px_vec3 direction, double distance, double delta_time, int jump, int squat, int grounded, double vertical_velocity, px_vec3* out_position, int* out_blocked, int* out_grounded, int* out_crouched, double* out_vertical_velocity, char* err, int err_len) {
    if (world == nullptr || out_position == nullptr || out_blocked == nullptr || out_grounded == nullptr || out_crouched == nullptr || out_vertical_velocity == nullptr) {
        set_error(err, err_len, "invalid move request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.controller == nullptr || iter->second.actor == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    if (!valid_vec3(direction) || !std::isfinite(distance) || distance < 0 || !std::isfinite(delta_time) || delta_time <= 0 || !std::isfinite(vertical_velocity)) {
        set_error(err, err_len, "invalid move value");
        return 1;
    }

    PxVec3 horizontal = to_px_vec3(direction);
    PxReal horizontal_length = horizontal.magnitude();
    if (horizontal_length > 0.0001f && distance > 0) {
        horizontal = horizontal / horizontal_length * static_cast<PxReal>(distance);
    } else {
        horizontal = PxVec3(0.0f);
    }

    // 保留原有跳跃初速度和重力规则，CCT 只负责碰撞推进
    double next_vertical_velocity = vertical_velocity;
    if (jump != 0 && grounded != 0) {
        next_vertical_velocity = k_player_jump_speed + k_player_gravity * delta_time;
    } else if (grounded != 0) {
        next_vertical_velocity = 0.0;
    } else {
        next_vertical_velocity += k_player_gravity * delta_time;
    }
    bool requested_crouched = squat != 0;
    if (requested_crouched != iter->second.crouched) {
        if (requested_crouched) {
            iter->second.controller->resize(capsule_controller_height(iter->second.radius, iter->second.crouch_height));
            iter->second.crouched = true;
            refresh_scene_queries(world->scene);
        } else {
            PxExtendedVec3 foot = iter->second.controller->getFootPosition();
            PxExtendedVec3 center = foot;
            center.y += static_cast<PxExtended>(iter->second.contact_offset + iter->second.height * 0.5);
            PxCapsuleGeometry standing_geometry(static_cast<PxReal>(iter->second.radius), capsule_controller_height(iter->second.radius, iter->second.height) * 0.5f);
            PxTransform standing_pose(PxVec3(static_cast<PxReal>(center.x), static_cast<PxReal>(center.y), static_cast<PxReal>(center.z)), iter->second.actor->getGlobalPose().q);
            if (!has_overlap(world->scene, standing_geometry, standing_pose, iter->second.actor)) {
                iter->second.controller->resize(capsule_controller_height(iter->second.radius, iter->second.height));
                iter->second.crouched = false;
                refresh_scene_queries(world->scene);
            }
        }
    }

    PxVec3 displacement = horizontal + PxVec3(0.0f, static_cast<PxReal>(next_vertical_velocity * delta_time), 0.0f);

    PxFilterData filter_data;
    IgnoreActorFilter filter_callback(iter->second.actor);
    IgnoreCCTFilter cct_filter;
    PxControllerFilters filters(&filter_data, &filter_callback, &cct_filter);

    // CCT move 内部完成 sweep、碰撞修正和 collide-and-slide，不再推进整个 scene
    PxControllerCollisionFlags collision_flags = iter->second.controller->move(
        displacement,
        0.0001f,
        static_cast<PxReal>(delta_time),
        filters
    );

    bool side_blocked = collision_flags.isSet(PxControllerCollisionFlag::eCOLLISION_SIDES);
    bool ceiling_blocked = collision_flags.isSet(PxControllerCollisionFlag::eCOLLISION_UP);
    bool is_grounded = collision_flags.isSet(PxControllerCollisionFlag::eCOLLISION_DOWN);
    if (ceiling_blocked && next_vertical_velocity > 0.0) {
        next_vertical_velocity = 0.0;
    }
    if (is_grounded && next_vertical_velocity <= 0.0) {
        next_vertical_velocity = 0.0;
    }

    *out_position = controller_player_position(iter->second.controller);
    *out_blocked = (side_blocked || ceiling_blocked) ? 1 : 0;
    *out_grounded = is_grounded ? 1 : 0;
    *out_crouched = iter->second.crouched ? 1 : 0;
    *out_vertical_velocity = next_vertical_velocity;
    return 0;
}

PHYSX_BRIDGE_API int px_world_get_player_position(px_world* world, uint64_t player_id, px_vec3* out_position, char* err, int err_len) {
    if (world == nullptr || out_position == nullptr) {
        set_error(err, err_len, "invalid get position request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.controller == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    *out_position = controller_player_position(iter->second.controller);
    return 0;
}

PHYSX_BRIDGE_API int px_world_set_player_position(px_world* world, uint64_t player_id, px_vec3 position, char* err, int err_len) {
    if (world == nullptr || !valid_vec3(position)) {
        set_error(err, err_len, "invalid set position request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.controller == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    // 重生属于传送，使用 CCT 的脚底坐标接口保持 Go 侧坐标契约
    if (!iter->second.controller->setFootPosition(controller_foot_position(position))) {
        set_error(err, err_len, "set player controller position failed");
        return 1;
    }
    // 重生同时恢复站立高度，避免逻辑状态与 PhysX 胶囊体姿态不一致
    if (iter->second.crouched) {
        iter->second.controller->resize(capsule_controller_height(iter->second.radius, iter->second.height));
        iter->second.crouched = false;
    }
    // CCT 内部会把传送结果提交给代理 actor，推进一次最小正时间步使 scene query 立即可见
    if (!world->scene->simulate(1.0e-6f)) {
        set_error(err, err_len, "update player controller scene failed");
        return 1;
    }
    if (!world->scene->fetchResults(true)) {
        set_error(err, err_len, "fetch player controller scene results failed");
        return 1;
    }
    refresh_scene_queries(world->scene);
    return 0;
}

PHYSX_BRIDGE_API int px_world_raycast(px_world* world, px_vec3 origin, px_vec3 direction, double max_distance, uint32_t mask, uint64_t ignored_player_id, px_raycast_hit* out_hit, char* err, int err_len) {
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

PHYSX_BRIDGE_API int px_world_batch_raycast(px_world* world, const px_vec3* origins, const px_vec3* directions, const double* max_distances, const uint32_t* masks, const uint64_t* ignored_player_ids, int count, px_raycast_hit* out_hits, char* err, int err_len) {
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
