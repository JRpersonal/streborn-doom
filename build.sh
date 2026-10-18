#!/bin/sh
# Builds everything into dist/:
#   dist/doom-str   DOOM for the speaker (ARMv5, soft-float, static musl)
#   dist/doompad    the panel, remote and sound tool for the speaker
#   dist/str-doom*  the launcher for your computer, with both of the above inside
#
# Needs Zig 0.13 (as the C cross compiler) and Go 1.25 or newer.
# ZIG and GO may point at the binaries; otherwise they come from PATH.
set -eu
cd "$(dirname "$0")"
ZIG=${ZIG:-zig}
GO=${GO:-go}
mkdir -p dist

ENGINE="dummy am_map doomdef doomstat dstrings d_event d_items d_iwad d_loop d_main d_mode d_net
f_finale f_wipe g_game hu_lib hu_stuff info i_cdmus i_endoom i_joystick i_scale i_sound i_system
i_timer memio m_argv m_bbox m_cheat m_config m_controls m_fixed m_menu m_misc m_random p_ceilng
p_doors p_enemy p_floor p_inter p_lights p_map p_maputl p_mobj p_plats p_pspr p_saveg p_setup
p_sight p_spec p_switch p_telept p_tick p_user r_bsp r_data r_draw r_main r_plane r_segs r_sky
r_things sha1 sounds statdump st_lib st_stuff s_sound tables v_video wi_stuff w_checksum w_file
w_main w_wad z_zone w_file_stdc i_input i_video doomgeneric mus2mid doomgeneric_str i_strsound"
OPL="i_oplmusic opl opl_queue opl_sdl dbopl midifile"

SRC=""
for f in $ENGINE; do SRC="$SRC engine/$f.c"; done
for f in $OPL; do SRC="$SRC opl/$f.c"; done

# ARMv5 without VFP: some SoundTouch CPUs have no working FPU (the same
# reason STR's agent is built with GOARM=5).
# shellcheck disable=SC2086
"$ZIG" cc -target arm-linux-musleabi -mcpu=arm926ej_s -Os \
  -ffunction-sections -fdata-sections -Wl,--gc-sections -s -static -w \
  -DNORMALUNIX -DLINUX -D_DEFAULT_SOURCE -DFEATURE_SOUND -DSTR_SOUND \
  -DDOOMGENERIC_RESX=320 -DDOOMGENERIC_RESY=200 \
  -Dmusic_opl_module=DG_music_module \
  -include stdlib.h -include string.h -include opl/shim/SDL.h -include opl/str_opl_compat.h \
  -Iopl/shim -Iengine -Iopl \
  $SRC -o dist/doom-str

(cd doompad && GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 "$GO" build -trimpath -ldflags="-s -w" -o ../dist/doompad .)

mkdir -p launcher/bin
cp dist/doom-str dist/doompad launcher/bin/
VERSION=${VERSION:-dev}
for target in windows/amd64 darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
  os=${target%/*}; arch=${target#*/}
  ext=""; [ "$os" = windows ] && ext=".exe"
  (cd launcher && GOOS=$os GOARCH=$arch CGO_ENABLED=0 "$GO" build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION" -o "../dist/str-doom-$os-$arch$ext" .)
done
ls -la dist
