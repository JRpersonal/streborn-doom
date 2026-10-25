DOOM on a Bose SoundTouch running STR. Download `str-doom` for your computer and run it with the speaker's address; see the README for the keys.

## v0.1.1

- On Windows you can now double-click `str-doom-windows-amd64.exe`. It asks for the speaker's IP address and keeps its window open until you press Enter.
- The Windows .exe is code-signed (Certum).
- The README has install steps for each system and a short "How it works".

### What you need

- A SoundTouch with a pixel display. That is the Portable (tested), the 20 or the 30 (same panel, untested). The Wave, 10 and 300 cannot run it.
- [STR](https://st-reborn.de) on the speaker, agent v0.9.0 or newer (v1.0.23 recommended).
- A computer on the same network, and internet on the first run for the shareware WAD.

### Install and run

- **Windows.** Double-click the .exe and type the speaker's address. If Windows shows a SmartScreen warning, click "More info", then "Run anyway".
- **macOS.** `chmod +x str-doom-darwin-arm64`, then `xattr -d com.apple.quarantine str-doom-darwin-arm64`, then `./str-doom-darwin-arm64 192.0.2.50`. Intel Macs use `str-doom-darwin-amd64`.
- **Linux.** `chmod +x str-doom-linux-amd64 && ./str-doom-linux-amd64 192.0.2.50`

| File | For |
|---|---|
| `str-doom-windows-amd64.exe` | Windows |
| `str-doom-darwin-arm64` / `str-doom-darwin-amd64` | macOS (Apple silicon / Intel) |
| `str-doom-linux-amd64` / `str-doom-linux-arm64` | Linux |
| `doom-str`, `doompad` | the speaker side, already inside `str-doom` |

The macOS and Linux programs are not code-signed.
