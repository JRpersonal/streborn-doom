#ifndef STR_SDL_MIXER_SHIM_H
#define STR_SDL_MIXER_SHIM_H
#include "SDL.h"
#define STR_MIX_RATE 11025
typedef str_mix_hook_fn str_mix_hook_t;
static inline int Mix_QuerySpec(int *freq, Uint16 *format, int *channels) {
  *freq = STR_MIX_RATE; *format = AUDIO_S16SYS; *channels = 2; return 1;
}
static inline int Mix_OpenAudio(int f, Uint16 fmt, int ch, int chunk) { (void)f; (void)fmt; (void)ch; (void)chunk; return 0; }
static inline void Mix_CloseAudio(void) {}
static inline const char *Mix_GetError(void) { return "no SDL"; }
static inline void Mix_HookMusic(str_mix_hook_t fn, void *arg) { (void)arg; str_music_hook = fn; }
#endif
