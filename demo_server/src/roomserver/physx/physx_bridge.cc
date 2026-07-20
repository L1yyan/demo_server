//go:build physx

#include "physx_bridge.h"

#include <PxPhysicsAPI.h>

#include <algorithm>
#include <cmath>
#include <cstring>
#include <unordered_map>

using namespace physx;

struct player_actor {
    PxRigidDynamic* actor;
    double radius;
    double height;
};

struct px_world {
    PxDefaultAllocator allocator;
    PxDefaultErrorCallback error_callback;
    PxFoundation* foundation;
    PxPhysics* physics;
    PxDefaultCpuDispatcher* dispatcher;
    PxScene* scene;
    PxMaterial* material;
    std::unordered_map<uint64_t, player_actor> players;
};

namespace {

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

px_vec3 from_px_vec3(const PxVec3& value) {
    return px_vec3{static_cast<double>(value.x), static_cast<double>(value.y), static_cast<double>(value.z)};
}

bool valid_vec3(px_vec3 value) {
    return std::isfinite(value.x) && std::isfinite(value.y) && std::isfinite(value.z);
}

class IgnoreActorFilter : public PxQueryFilterCallback {
public:
    explicit IgnoreActorFilter(const PxRigidActor* ignored_actor) : ignored_actor_(ignored_actor) {}

    PxQueryHitType::Enum preFilter(const PxFilterData&, const PxShape*, const PxRigidActor* actor, PxHitFlags&) override {
        if (actor == ignored_actor_) {
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

} // namespace

extern "C" {

px_world* px_world_create(int create_ground_plane, char* err, int err_len) {
    px_world* world = new px_world{};
    world->foundation = PxCreateFoundation(PX_PHYSICS_VERSION, world->allocator, world->error_callback);
    if (world->foundation == nullptr) {
        set_error(err, err_len, "create foundation failed");
        delete world;
        return nullptr;
    }

    world->physics = PxCreatePhysics(PX_PHYSICS_VERSION, *world->foundation, PxTolerancesScale(), true, nullptr);
    if (world->physics == nullptr) {
        set_error(err, err_len, "create physics failed");
        world->foundation->release();
        delete world;
        return nullptr;
    }

    world->dispatcher = PxDefaultCpuDispatcherCreate(1);
    if (world->dispatcher == nullptr) {
        set_error(err, err_len, "create dispatcher failed");
        world->physics->release();
        world->foundation->release();
        delete world;
        return nullptr;
    }

    PxSceneDesc scene_desc(world->physics->getTolerancesScale());
    scene_desc.gravity = PxVec3(0.0f, -9.81f, 0.0f);
    scene_desc.cpuDispatcher = world->dispatcher;
    scene_desc.filterShader = PxDefaultSimulationFilterShader;
    world->scene = world->physics->createScene(scene_desc);
    if (world->scene == nullptr) {
        set_error(err, err_len, "create scene failed");
        world->dispatcher->release();
        world->physics->release();
        world->foundation->release();
        delete world;
        return nullptr;
    }

    world->material = world->physics->createMaterial(0.5f, 0.5f, 0.6f);
    if (world->material == nullptr) {
        set_error(err, err_len, "create material failed");
        world->scene->release();
        world->dispatcher->release();
        world->physics->release();
        world->foundation->release();
        delete world;
        return nullptr;
    }

    if (create_ground_plane != 0) {
        PxRigidStatic* plane = PxCreatePlane(*world->physics, PxPlane(0, 1, 0, 0), *world->material);
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
    if (world->material != nullptr) {
        world->material->release();
    }
    if (world->scene != nullptr) {
        world->scene->release();
    }
    if (world->dispatcher != nullptr) {
        world->dispatcher->release();
    }
    if (world->physics != nullptr) {
        world->physics->release();
    }
    if (world->foundation != nullptr) {
        world->foundation->release();
    }
    delete world;
}

int px_world_add_player_capsule(px_world* world, uint64_t player_id, px_vec3 position, double radius, double height, char* err, int err_len) {
    if (world == nullptr || world->physics == nullptr || world->scene == nullptr || world->material == nullptr) {
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
    PxRigidDynamic* actor = PxCreateDynamic(*world->physics, player_transform(position, radius, height), geometry, *world->material, 1.0f);
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

int px_world_move_player(px_world* world, uint64_t player_id, px_vec3 direction, double distance, px_vec3* out_position, int* out_blocked, char* err, int err_len) {
    if (world == nullptr || out_position == nullptr || out_blocked == nullptr) {
        set_error(err, err_len, "invalid move request");
        return 1;
    }
    auto iter = world->players.find(player_id);
    if (iter == world->players.end() || iter->second.actor == nullptr) {
        set_error(err, err_len, "player not found");
        return 1;
    }
    if (!valid_vec3(direction) || !std::isfinite(distance) || distance < 0) {
        set_error(err, err_len, "invalid move value");
        return 1;
    }

    PxRigidDynamic* actor = iter->second.actor;
    PxVec3 dir = to_px_vec3(direction);
    PxReal length = dir.magnitude();
    if (length <= 0.0001f || distance == 0) {
        *out_position = actor_player_position(actor, iter->second.height);
        *out_blocked = 0;
        return 0;
    }
    dir /= length;

    PxCapsuleGeometry geometry(static_cast<PxReal>(iter->second.radius), capsule_half_height(iter->second.radius, iter->second.height));
    PxTransform current = actor->getGlobalPose();
    PxSweepBuffer sweep_hit;
    PxQueryFilterData filter_data(PxQueryFlag::eSTATIC | PxQueryFlag::eDYNAMIC | PxQueryFlag::ePREFILTER);
    IgnoreActorFilter filter_callback(actor);
    bool blocked = world->scene->sweep(geometry, current, dir, static_cast<PxReal>(distance), sweep_hit, PxHitFlag::eDEFAULT, filter_data, &filter_callback);

    PxReal travel = static_cast<PxReal>(distance);
    if (blocked && sweep_hit.hasBlock) {
        travel = std::max<PxReal>(0.0f, sweep_hit.block.distance - 0.01f);
    }
    PxTransform next = current;
    next.p += dir * travel;
    actor->setKinematicTarget(next);
    world->scene->simulate(1.0f / 60.0f);
    world->scene->fetchResults(true);
    actor->setGlobalPose(next);

    *out_position = actor_player_position(actor, iter->second.height);
    *out_blocked = blocked ? 1 : 0;
    return 0;
}

int px_world_raycast(px_world* world, px_vec3 origin, px_vec3 direction, double max_distance, uint32_t mask, px_raycast_hit* out_hit, char* err, int err_len) {
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

    PxRaycastBuffer hit;
    PxQueryFilterData filter_data(PxQueryFlag::eSTATIC | PxQueryFlag::eDYNAMIC);
    bool has_hit = world->scene->raycast(to_px_vec3(origin), dir, static_cast<PxReal>(max_distance), hit, PxHitFlag::eDEFAULT, filter_data);
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

int px_world_batch_raycast(px_world* world, const px_vec3* origins, const px_vec3* directions, const double* max_distances, const uint32_t* masks, int count, px_raycast_hit* out_hits, char* err, int err_len) {
    if (world == nullptr || origins == nullptr || directions == nullptr || max_distances == nullptr || masks == nullptr || out_hits == nullptr || count < 0) {
        set_error(err, err_len, "invalid batch raycast request");
        return 1;
    }
    for (int i = 0; i < count; ++i) {
        int code = px_world_raycast(world, origins[i], directions[i], max_distances[i], masks[i], &out_hits[i], err, err_len);
        if (code != 0) {
            return code;
        }
    }
    return 0;
}

} // extern "C"
