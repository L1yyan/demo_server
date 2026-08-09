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
    PxRigidDynamic* actor;
    double radius;
    double height;
};

struct px_runtime {
    PxDefaultAllocator allocator;
    PxDefaultErrorCallback error_callback;
    PxFoundation* foundation;
    PxPhysics* physics;
    PxPvd* pvd;
    PxPvdTransport* pvd_transport;
    bool pvd_enabled;
    bool extensions_initialized;
    int ref_count;
};

struct px_world {
    px_runtime* runtime;
    PxDefaultCpuDispatcher* dispatcher;
    PxScene* scene;
    PxMaterial* material;
    std::unordered_map<uint64_t, player_actor> players;
    std::vector<PxRigidStatic*> static_actors;
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

class IgnoreActorFilter : public PxQueryFilterCallback {
public:
    explicit IgnoreActorFilter(const PxRigidActor* ignored_actor) : ignored_actor_(ignored_actor) {}

    PxQueryHitType::Enum preFilter(const PxFilterData&, const PxShape*, const PxRigidActor* actor, PxHitFlags&) override {
        if (ignored_actor_ != nullptr && actor == ignored_actor_) {
            return PxQueryHitType::eNONE;
        }
        return PxQueryHitType::eBLOCK;
    }

    PxQueryHitType::Enum postFilter(const PxFilterData&, const PxQueryHit&, const PxShape*, const PxRigidActor*) override {
        return PxQueryHitType::eNONE;
    }

private:
    const PxRigidActor* ignored_actor_;
};

PxReal capsule_half_height(double radius, double height) {
    return static_cast<PxReal>(std::max(0.01, (height - radius * 2.0) * 0.5));
}

PxTransform player_transform(px_vec3 position, double radius, double height) {
    PxReal center_y = static_cast<PxReal>(position.y + height * 0.5);
    return PxTransform(PxVec3(static_cast<PxReal>(position.x), center_y, static_cast<PxReal>(position.z)), PxQuat(PxHalfPi, PxVec3(0, 0, 1)));
}

px_vec3 actor_player_position(PxRigidDynamic* actor, double height) {
    PxTransform pose = actor->getGlobalPose();
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
        if (pvd_config.enabled != 0 && !g_runtime->pvd_enabled) {
            set_error(err, err_len, "pvd must be enabled before physx runtime is created");
            return nullptr;
        }
        ++g_runtime->ref_count;
        return g_runtime;
    }

    px_runtime* runtime = new px_runtime{};
    runtime->foundation = PxCreateFoundation(PX_PHYSICS_VERSION, runtime->allocator, runtime->error_callback);
    if (runtime->foundation == nullptr) {
        set_error(err, err_len, "create foundation failed");
        delete runtime;
        return nullptr;
    }

    if (!init_pvd(runtime, pvd_config, err, err_len)) {
        cleanup_runtime(runtime);
        delete runtime;
        return nullptr;
    }

    runtime->physics = PxCreatePhysics(PX_PHYSICS_VERSION, *runtime->foundation, PxTolerancesScale(), true, runtime->pvd);
    if (runtime->physics == nullptr) {
        set_error(err, err_len, "create physics failed");
        cleanup_runtime(runtime);
        delete runtime;
        return nullptr;
    }
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
