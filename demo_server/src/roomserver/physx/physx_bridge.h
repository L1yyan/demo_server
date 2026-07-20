#ifndef DEMO_SERVER_ROOMSERVER_PHYSX_BRIDGE_H
#define DEMO_SERVER_ROOMSERVER_PHYSX_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct px_world px_world;

typedef struct px_vec3 {
    double x;
    double y;
    double z;
} px_vec3;

typedef struct px_raycast_hit {
    int hit;
    uint64_t target_id;
    px_vec3 point;
    px_vec3 normal;
    double distance;
} px_raycast_hit;

px_world* px_world_create(int create_ground_plane, char* err, int err_len);
void px_world_release(px_world* world);
int px_world_add_player_capsule(px_world* world, uint64_t player_id, px_vec3 position, double radius, double height, char* err, int err_len);
int px_world_remove_player(px_world* world, uint64_t player_id, char* err, int err_len);
int px_world_move_player(px_world* world, uint64_t player_id, px_vec3 direction, double distance, px_vec3* out_position, int* out_blocked, char* err, int err_len);
int px_world_raycast(px_world* world, px_vec3 origin, px_vec3 direction, double max_distance, uint32_t mask, px_raycast_hit* out_hit, char* err, int err_len);
int px_world_batch_raycast(px_world* world, const px_vec3* origins, const px_vec3* directions, const double* max_distances, const uint32_t* masks, int count, px_raycast_hit* out_hits, char* err, int err_len);

#ifdef __cplusplus
}
#endif

#endif
