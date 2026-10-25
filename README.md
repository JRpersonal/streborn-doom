# DOOM on a Bose SoundTouch

The original DOOM (shareware 1.9) on a Bose SoundTouch speaker: drawn on the speaker's own 128 by 100 display, played with its IR remote, with the sound effects and the original music coming out of the speaker.

This is a side project of [STR (SoundTouch Reborn)](https://github.com/JRpersonal/streborn), which keeps these speakers working after Bose switched off their cloud. **DOOM is not part of STR** and is never installed by it. It is here for people who want to try it.

<!-- VIDEO: drag the V3 clip here -->

## What you need

- A SoundTouch with a pixel display. That is the **Portable** (tested), the **SoundTouch 20** or the **30** (same panel, untested; see [Other models](#other-models)). The Wave has a segment display, and the SoundTouch 10 and 300 have no display, so DOOM cannot run on them.
- [STR](https://st-reborn.de) on the speaker, agent v0.9.0 or newer (v1.0.23 recommended). The speaker must be awake.
- A computer on the same network as the speaker (Windows, macOS or Linux).
- Internet on the first run, for the shareware WAD.
- The speaker's IP address (the STR app shows it).

## Install and run

Download `str-doom` for your computer from the [latest release](https://github.com/JRpersonal/streborn-doom/releases/latest).

**Windows.** Double-click `str-doom-windows-amd64.exe` (v0.1.1 or newer) and type the speaker's address when it asks. If Windows shows a SmartScreen warning, click "More info", then "Run anyway". Or in PowerShell, from your Downloads folder:

```
.\str-doom-windows-amd64.exe 192.0.2.50
```

**macOS.** In Terminal, from your Downloads folder (Intel Macs use `str-doom-darwin-amd64`):

```
chmod +x str-doom-darwin-arm64
xattr -d com.apple.quarantine str-doom-darwin-arm64
./str-doom-darwin-arm64 192.0.2.50
```

**Linux.**

```
chmod +x str-doom-linux-amd64
./str-doom-linux-amd64 192.0.2.50
```

The examples below call it `str-doom`. The first run downloads the free shareware `DOOM1.WAD` from Debian's archive (checked against its SHA-256) and keeps it in your cache folder. Then the speaker shows the DOOM title screen and plays the intro music. The program on your computer is done at that point; the game keeps running on the speaker.

Hold the remote **sideways**:

| Key | In the game |
|---|---|
| 1 / volume up | turn left / right |
| 5 / 4 | forward / back |
| skip back | fire |
| skip | open doors, use switches; on the title screen: start a game |
| thumbs up | map |
| thumbs down | next weapon |
| power | end the game |

While you play, the speaker does nothing with these keys, and neither does STR. Power ends the game and gives the display and the keys back.

Other options:

```
str-doom -vol 30 192.0.2.50      speaker volume while playing (default 20)
str-doom -wad DOOM.WAD 192.0.2.50  play your own WAD (registered DOOM, Freedoom, ...)
str-doom stop 192.0.2.50         end a game from the computer
str-doom learn 192.0.2.50        show the codes of your remote's keys
```

## What it does to the speaker

Nothing permanent. Everything goes into the speaker's RAM (`/dev/shm/doom`), and a restart of the speaker removes it. While a game runs:

- BoseApp's display thread is paused so the game owns the panel, and its key thread is watched so remote keys go to the game instead of the firmware.
- The speaker's audio server, which normally holds every stream 1.8 s back for multiroom sync, is set to 350 ms. Below about 300 ms it goes silent.
- DOOM and its helper get CPU priority over STR's background processes.

All of it is undone when the game ends. To get there it uses SSH, which STR can switch on for the speaker; `str-doom` asks STR to do that.

## How it works

- `str-doom` asks STR to switch on SSH, copies DOOM and its helper into the speaker's RAM and exits. The game runs on the speaker's own ARM CPU.
- BoseApp reads the remote keys from `/dev/ir`. While you play, that read gets `-EAGAIN` back through ptrace, so the firmware never sees a press, and the helper hands the key to DOOM.
- The audio server holds every stream 1802 ms back for multiroom sync. The helper lowers that target in the running process to about 350 ms. Below about 300 ms the DSP mutes. The old value comes back on exit.
- The helper is Go. A Go 1.26 binary that imports crypto reserves 32 MB of BSS, and the speaker's 3.14 kernel SIGKILLs it at exec, so the on-box tools avoid `net/http` and `crypto`.
- For the music, Nuked OPL3 took 152% of one CPU core on this chip. DOSBox's dbopl needs about 3%, so that is what plays.
- To build it yourself, see [How it is built](#how-it-is-built).

## Other models

The SoundTouch 20 and 30 have the same 128 by 100 display and may just work. Their remotes may send other key codes: run `str-doom learn <ip>`, press each key once, and pass the codes the game needs with `-ir`, for example `-ir e7=3,27=24`. Then please tell me what you found with the ["Tried it on my model"](https://github.com/JRpersonal/streborn-doom/issues/new?template=tried-it.yml) form.

## How it is built

| Part | What it is |
|---|---|
| `engine/` | [doomgeneric](https://github.com/ozkl/doomgeneric) with a SoundTouch backend (`doomgeneric_str.c`) and sound module (`i_strsound.c`) |
| `opl/` | the OPL music player and DOSBox OPL emulator from [Chocolate Doom](https://github.com/chocolate-doom/chocolate-doom) 2.2.1, with a minimal SDL stand-in |
| `doompad/` | runs on the speaker: owns the display and the remote keys, streams the sound, restores everything afterwards |
| `launcher/` | `str-doom`, the program for your computer |

`build.sh` builds it all with Zig (as the ARM C compiler) and Go. DOOM is built for ARMv5 without floating point hardware, like STR's own agent.

## Licence

GPL version 2, see [LICENSE](LICENSE). DOOM's source is © id Software and released under the GPL; doomgeneric, Chocolate Doom and the DOSBox OPL emulator are GPL as well. The shareware `DOOM1.WAD` is not part of this repository.

DOOM is a trademark of id Software. This project is not affiliated with id Software.

Bose and SoundTouch are registered trademarks of Bose Corporation. Unofficial, not affiliated with or endorsed by Bose.

## Questions

Ask in the [STR Arcade discussions](https://github.com/JRpersonal/streborn/discussions/categories/arcade).
