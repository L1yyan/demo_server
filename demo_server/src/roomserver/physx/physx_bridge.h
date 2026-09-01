#ifndef DEMO_SERVER_ROOMSERVER_PHYSX_BRIDGE_H
#define DEMO_SERVER_ROOMSERVER_PHYSX_BRIDGE_H

#include <stdint.h>

#if defined(_WIN32) && defined(PHYSX_BRIDGE_EXPORTS)
#define PHYSX_BRIDGE_API __declspec(dllexport)
#elif defined(_WIN32)
#define PHYSX_BRIDGE_API __declspec(dllimport)
#else
#define PHYSX_BRIDGE_API
#endif

#ifdef __cplusplus
extern "C" {
#endif

typedef struct px_world px_world;

typedef struct px_vec3 {
    double x;
    double y;
    double z;
} px_vec3;

typedef struct px_quat {
    double x;
    double y;
    double z;
    double w;
} px_quat;

typedef struct px_raycast_hit {
    int hit;
    uint64_t target_id;
    px_vec3 point;
    px_vec3 normal;
    double distance;
} px_raycast_hit;

typedef struct px_pvd_config {
    int enabled;
    const char* host;
    int port;
    unsigned int timeout_ms;
} px_pvd_config;

PHYSX_BRIDGE_API px_world* px_world_create(int create_ground_plane, px_pvd_config pvd_config, char* err, int err_len);
PHYSX_BRIDGE_API void px_world_release(px_world* world);
PHYSX_BRIDGE_API int px_world_add_static_box(px_world* world, px_vec3 position, px_quat rotation, px_vec3 size, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_add_static_mesh(px_world* world, const double* vertices, int vertex_count, const int* triangles, int triangle_count, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_add_player_capsule(px_world* world, uint64_t player_id, px_vec3 position, double radius, double height, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_remove_player(px_world* world, uint64_t player_id, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_move_player(px_world* world, uint64_t player_id, px_vec3 direction, double distance, double delta_time, int jump, int squat, int grounded, double vertical_velocity, px_vec3* out_position, int* out_blocked, int* out_grounded, int* out_crouched, double* out_vertical_velocity, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_get_player_position(px_world* world, uint64_t player_id, px_vec3* out_position, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_set_player_position(px_world* world, uint64_t player_id, px_vec3 position, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_raycast(px_world* world, px_vec3 origin, px_vec3 direction, double max_distance, uint32_t mask, uint64_t ignored_player_id, px_raycast_hit* out_hit, char* err, int err_len);
PHYSX_BRIDGE_API int px_world_batch_raycast(px_world* world, const px_vec3* origins, const px_vec3* directions, const double* max_distances, const uint32_t* masks, const uint64_t* ignored_player_ids, int count, px_raycast_hit* out_hits, char* err, int err_len);

#ifdef __cplusplus
}
#endif

#endif
