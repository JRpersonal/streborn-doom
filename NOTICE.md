# Sources and changes

## engine/

From [ozkl/doomgeneric](https://github.com/ozkl/doomgeneric) at commit `e257a09527062bfb986b6e5e0780839b6cbd5b71` (GPL v2). Changes:

- added `doomgeneric_str.c`: the SoundTouch backend (frames scaled to 128x80 luma on fd 3, keys on stdin, frame cap, music volume, next-weapon key, optional low detail)
- added `i_strsound.c`: sound effects mixer and music hook, 11025 Hz stereo on fd 4
- `i_sound.c`: the SDL_mixer include is skipped when `STR_SOUND` is defined
- removed the other platform backends (SDL, X11, Allegro, Windows, Emscripten, soso)

## opl/

From [chocolate-doom](https://github.com/chocolate-doom/chocolate-doom) tag `chocolate-doom-2.2.1` (GPL v2): `opl.c`, `opl.h`, `opl_internal.h`, `opl_queue.c`, `opl_queue.h`, `opl_sdl.c`, `dbopl.c`, `dbopl.h` (the DOSBox OPL emulator, © The DOSBox Team), `i_oplmusic.c`, `midifile.c`, `midifile.h`. Unchanged. Added:

- `shim/SDL.h`, `shim/SDL_mixer.h`: a minimal stand-in so the OPL driver runs inside DOOM's game loop
- `str_opl_compat.h`: one type `i_oplmusic.c` expects from Chocolate Doom's `i_sound.h`

## doompad/, launcher/

Written for this project, GPL v2.
