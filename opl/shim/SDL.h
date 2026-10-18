// Just enough of SDL for Chocolate Doom's OPL driver on the SoundTouch spike.
// Doom runs single-threaded here and the "audio callback" is called from the
// game loop (i_strsound.c), so the mutexes have nothing to protect.
#ifndef STR_SDL_SHIM_H
#define STR_SDL_SHIM_H
#include <stdint.h>
#include <stdlib.h>
static inline uint32_t SDL_SwapBE32(uint32_t x) { return __builtin_bswap32(x); }
static inline uint16_t SDL_SwapBE16(uint16_t x) { return __builtin_bswap16(x); }
typedef uint8_t Uint8;
typedef uint16_t Uint16;
typedef uint32_t Uint32;
typedef struct { int unused; } SDL_mutex;
#define SDL_INIT_AUDIO 0x10
#define AUDIO_S16SYS 0x8010
static inline SDL_mutex *SDL_CreateMutex(void) { static SDL_mutex m; return &m; }
static inline void SDL_DestroyMutex(SDL_mutex *m) { (void)m; }
static inline int SDL_LockMutex(SDL_mutex *m) { (void)m; return 0; }
static inline int SDL_UnlockMutex(SDL_mutex *m) { (void)m; return 0; }
static inline int SDL_Init(Uint32 f) { (void)f; return 0; }
static inline void SDL_QuitSubSystem(Uint32 f) { (void)f; }
static inline void SDL_PauseAudio(int p) { (void)p; }
// OPL_Delay waits on a condition that an OPL callback signals. With no audio
// thread, waiting means rendering: advance the emulator a little.
typedef struct { int unused; } SDL_cond;
typedef void (*str_mix_hook_fn)(void *udata, Uint8 *stream, int len);
extern str_mix_hook_fn str_music_hook;
static inline SDL_cond *SDL_CreateCond(void) { static SDL_cond c; return &c; }
static inline void SDL_DestroyCond(SDL_cond *c) { (void)c; }
static inline int SDL_CondSignal(SDL_cond *c) { (void)c; return 0; }
static inline int SDL_CondWait(SDL_cond *c, SDL_mutex *m) {
  (void)c; (void)m;
  static Uint8 scratch[256];
  if (str_music_hook) str_music_hook(0, scratch, sizeof scratch);
  return 0;
}
#endif
