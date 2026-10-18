// Bits Chocolate Doom 2.2.1's i_oplmusic.c expects from its own i_sound.h.
#ifndef STR_OPL_COMPAT_H
#define STR_OPL_COMPAT_H
typedef enum { opl_v_old, opl_v_new } opl_driver_ver_t;
void I_SetOPLDriverVer(opl_driver_ver_t ver);
#endif
