// doomgeneric backend for the SoundTouch Portable spike.
//
// Doom renders 320x200 RGB into DG_ScreenBuffer. Each frame is reduced here to
// 128x80 luma (box filter, 2.5:1 both ways) and written to fd 3, 10240 bytes
// per frame, where the panel tool (oledfun -mode doom) picks it up. Keys come
// in on stdin as two bytes each: pressed (0/1), doom key code.
#include "doomkeys.h"
#include "doomgeneric.h"

#include <errno.h>
#include <stdlib.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#define OUT_W 128
#define OUT_H 80
#define FRAME_FD 3

static unsigned char out[OUT_W * OUT_H];
static int lumx[OUT_W + 1], lumy[OUT_H + 1];

void DG_Init(void) {
  for (int i = 0; i <= OUT_W; i++) lumx[i] = i * DOOMGENERIC_RESX / OUT_W;
  for (int i = 0; i <= OUT_H; i++) lumy[i] = i * DOOMGENERIC_RESY / OUT_H;
  fcntl(0, F_SETFL, fcntl(0, F_GETFL) | O_NONBLOCK);
  // The remote has no weapon keys to spare: one key cycles them. Set after
  // the config was loaded, so a saved config cannot unbind it.
  extern int key_nextweapon;
  key_nextweapon = 'n';
  // Low detail (Doom's own F5 mode) renders every other column: nearly half
  // the rendering cost, and the 128 px panel cannot show the difference.
  extern int detailLevel;
  if (getenv("DOOM_LOWDETAIL")) detailLevel = 1;
  // Music at full level: the speaker is turned down while playing, and the
  // OPL output is quiet next to the effects.
  extern int musicVolume;
  extern void S_SetMusicVolume(int volume);
  musicVolume = 15;
  S_SetMusicVolume(musicVolume * 8);
}

uint32_t DG_GetTicksMs(void);

// Doom's game logic runs on 35 tics per second whatever the frame rate, so
// drawing faster only burns the box's single core.
#define FRAME_MS 28

void DG_DrawFrame(void) {
  static uint32_t last;
  uint32_t now = DG_GetTicksMs();
  if (last && now - last < FRAME_MS) usleep((FRAME_MS - (now - last)) * 1000);
  last = DG_GetTicksMs();
  for (int oy = 0; oy < OUT_H; oy++) {
    for (int ox = 0; ox < OUT_W; ox++) {
      unsigned sum = 0, n = 0;
      for (int y = lumy[oy]; y < lumy[oy + 1]; y++) {
        const pixel_t *row = DG_ScreenBuffer + y * DOOMGENERIC_RESX;
        for (int x = lumx[ox]; x < lumx[ox + 1]; x++) {
          pixel_t c = row[x];
          // integer Rec. 601 luma, the box has no FPU worth using
          sum += (((c >> 16) & 0xff) * 77 + ((c >> 8) & 0xff) * 150 + (c & 0xff) * 29) >> 8;
          n++;
        }
      }
      out[oy * OUT_W + ox] = (unsigned char)(sum / n);
    }
  }
  const unsigned char *p = out;
  size_t left = sizeof out;
  while (left > 0) {
    ssize_t w = write(FRAME_FD, p, left);
    if (w < 0) {
      if (errno == EINTR) continue;
      _exit(0);  // the panel tool went away
    }
    p += w;
    left -= (size_t)w;
  }
}

void DG_SleepMs(uint32_t ms) { usleep(ms * 1000); }

uint32_t DG_GetTicksMs(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint32_t)(ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
}

int DG_GetKey(int *pressed, unsigned char *key) {
  unsigned char ev[2];
  ssize_t r = read(0, ev, 2);
  if (r != 2) return 0;
  *pressed = ev[0];
  *key = ev[1];
  return 1;
}

void DG_SetWindowTitle(const char *title) { (void)title; }

int main(int argc, char **argv) {
  doomgeneric_Create(argc, argv);
  for (;;) doomgeneric_Tick();
  return 0;
}
