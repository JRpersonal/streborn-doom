// Sound for the SoundTouch spike: Doom's own sound effects, mixed here and
// written as 11025 Hz stereo s16le to fd 4. The panel tool (doompad) streams
// them to the speaker. Paced by the wall clock: every Update writes exactly
// the samples that became due since the last one, so nothing is ever produced
// ahead of time (ahead means late on the speaker).
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>

#include "doomtype.h"
#include "i_sound.h"
#include "deh_str.h"
#include "m_misc.h"
#include "w_wad.h"
#include "z_zone.h"
#include "doomgeneric.h"
#include "SDL_mixer.h"

#define RATE 11025
#define NUM_CHANNELS 16
#define SOUND_FD 4
#define MAX_CHUNK 4096
// Produced this far ahead of the clock, so a slow frame does not leave a hole
// in the music: the panel tool keeps it as a jitter buffer.
#define LEAD_SAMPLES (RATE / 10)

typedef struct {
  const byte *data;  // unsigned 8 bit samples
  unsigned len;      // samples
  unsigned pos;      // 16.16 fixed
  unsigned step;     // 16.16 fixed
  int lv, rv;        // 0..255
  boolean on;
} chan_t;

static chan_t chans[NUM_CHANNELS];
static boolean use_prefix;
static uint32_t start_ms;
static uint64_t produced;
static boolean ok;

static boolean Init(boolean use_sfx_prefix) {
  use_prefix = use_sfx_prefix;
  if (fcntl(SOUND_FD, F_GETFL) < 0) return false;  // started without a sound pipe
  fcntl(SOUND_FD, F_SETFL, fcntl(SOUND_FD, F_GETFL) | O_NONBLOCK);
  start_ms = DG_GetTicksMs();
  ok = true;
  return true;
}

static void Shutdown(void) { ok = false; }

static int GetSfxLumpNum(sfxinfo_t *sfx) {
  char name[9];
  if (sfx->link != NULL) sfx = sfx->link;
  if (use_prefix)
    M_snprintf(name, sizeof name, "ds%s", DEH_String(sfx->name));
  else
    M_StringCopy(name, DEH_String(sfx->name), sizeof name);
  return W_GetNumForName(name);
}

static void SetParams(int ch, int vol, int sep) {
  if (ch < 0 || ch >= NUM_CHANNELS) return;
  // vol 0..127, sep 0 (left) .. 254 (right)
  chans[ch].lv = ((254 - sep) * vol) / 127;
  chans[ch].rv = (sep * vol) / 127;
  if (chans[ch].lv > 255) chans[ch].lv = 255;
  if (chans[ch].rv > 255) chans[ch].rv = 255;
}

static int StartSound(sfxinfo_t *sfx, int ch, int vol, int sep) {
  if (!ok || ch < 0 || ch >= NUM_CHANNELS) return -1;
  int lump = sfx->lumpnum;
  if (lump < 0) return -1;
  const byte *d = W_CacheLumpNum(lump, PU_STATIC);
  int len = W_LumpLength(lump);
  // DMX: u16 format (3), u16 rate, u32 sample count, 16 pad bytes each side
  if (len < 8 || d[0] != 3 || d[1] != 0) return -1;
  unsigned rate = d[2] | d[3] << 8;
  unsigned n = d[4] | d[5] << 8 | d[6] << 16 | (unsigned)d[7] << 24;
  if (n > (unsigned)len - 8) n = len - 8;
  if (n <= 32) return -1;
  chans[ch].data = d + 8 + 16;
  chans[ch].len = n - 32;
  chans[ch].pos = 0;
  chans[ch].step = (unsigned)(((uint64_t)rate << 16) / RATE);
  SetParams(ch, vol, sep);
  chans[ch].on = true;
  return ch;
}

static void StopSound(int ch) {
  if (ch >= 0 && ch < NUM_CHANNELS) chans[ch].on = false;
}

static boolean IsPlaying(int ch) {
  return ch >= 0 && ch < NUM_CHANNELS && chans[ch].on;
}

static void Update(void) {
  if (!ok) return;
  uint64_t due = (uint64_t)(DG_GetTicksMs() - start_ms) * RATE / 1000 + LEAD_SAMPLES;
  if (due <= produced) return;
  unsigned n = (unsigned)(due - produced);
  if (n > MAX_CHUNK) n = MAX_CHUNK;  // after a long stall: catch up over several frames
  produced += n;
  static int16_t out[MAX_CHUNK * 2];
  static int16_t mus[MAX_CHUNK * 2];
  if (str_music_hook)
    str_music_hook(NULL, (Uint8 *)mus, n * 4);
  else
    memset(mus, 0, n * 4);
  for (unsigned i = 0; i < n; i++) {
    int l = 0, r = 0;
    for (int c = 0; c < NUM_CHANNELS; c++) {
      chan_t *ch = &chans[c];
      if (!ch->on) continue;
      unsigned p = ch->pos >> 16;
      if (p >= ch->len) {
        ch->on = false;
        continue;
      }
      int s = ((int)ch->data[p] - 128) << 8;
      l += s * ch->lv >> 9;  // half scale: headroom for several effects at once
      r += s * ch->rv >> 9;
      ch->pos += ch->step;
    }
    l += mus[2 * i] * 3 / 2;  // OPL comes out quiet next to the effects
    r += mus[2 * i + 1] * 3 / 2;
    if (l > 32767) l = 32767; else if (l < -32768) l = -32768;
    if (r > 32767) r = 32767; else if (r < -32768) r = -32768;
    out[2 * i] = (int16_t)l;
    out[2 * i + 1] = (int16_t)r;
  }
  // Written in full: a partial frame would shift every sample after it.
  const unsigned char *p = (const unsigned char *)out;
  size_t left = n * 4;
  while (left > 0) {
    ssize_t w = write(SOUND_FD, p, left);
    if (w < 0) {
      if (errno == EINTR) continue;
      if (errno == EAGAIN) { usleep(1000); continue; }
      ok = false;
      return;
    }
    p += w;
    left -= (size_t)w;
  }
}

static void Precache(sfxinfo_t *sounds, int num) {
  for (int i = 0; i < num; i++) {
    char name[9];
    sfxinfo_t *s = sounds[i].link ? sounds[i].link : &sounds[i];
    M_snprintf(name, sizeof name, use_prefix ? "ds%s" : "%s", DEH_String(s->name));
    sounds[i].lumpnum = W_CheckNumForName(name);
  }
}

static snddevice_t devices[] = {SNDDEVICE_SB, SNDDEVICE_PAS, SNDDEVICE_GUS,
                                SNDDEVICE_WAVEBLASTER, SNDDEVICE_SOUNDCANVAS,
                                SNDDEVICE_AWE32};

sound_module_t DG_sound_module = {
    devices, sizeof devices / sizeof *devices,
    Init, Shutdown, GetSfxLumpNum, Update, SetParams,
    StartSound, StopSound, IsPlaying, Precache,
};

// Music: Chocolate Doom's OPL player (i_oplmusic.c, built as DG_music_module)
// drives the DOSBox OPL emulator through a stand-in for SDL_mixer's music
// hook. The hook is called from Update above, in the game loop.
str_mix_hook_t str_music_hook;


// i_sound.c binds these config keys; there is no resampler here.
int use_libsamplerate = 0;
float libsamplerate_scale = 0.65f;
