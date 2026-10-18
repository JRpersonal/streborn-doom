package main

// The speaker's audio server (APServer) holds every network stream
// wifiBufferTarget ms behind (1802 on the Portable, APConfig-taigan.xml), for
// multiroom sync. Its config file sits on the read-only rootfs, and restarting
// APServer reboots the box (Shepherd recovery="reboot"). So the parsed value
// is looked up in APServer's memory instead: the three numbers of that config
// line (46875 Hz, 1802 ms, 901 ms) sit close together.

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

type memRegion struct{ start, end uint64 }

func rwRegions(pid int) []memRegion {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return nil
	}
	var out []memRegion
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.HasPrefix(f[1], "rw") {
			continue
		}
		if len(f) >= 6 && strings.HasPrefix(f[5], "/dev/") {
			continue
		}
		ab := strings.SplitN(f[0], "-", 2)
		s, _ := strconv.ParseUint(ab[0], 16, 64)
		e, _ := strconv.ParseUint(ab[1], 16, 64)
		out = append(out, memRegion{s, e})
	}
	return out
}

// encodings of a number as APServer might keep it
func encodings(v float64) map[string][]byte {
	m := map[string][]byte{}
	i := make([]byte, 4)
	binary.LittleEndian.PutUint32(i, uint32(v))
	m["i32"] = i
	f := make([]byte, 4)
	binary.LittleEndian.PutUint32(f, math.Float32bits(float32(v)))
	m["f32"] = f
	d := make([]byte, 8)
	binary.LittleEndian.PutUint64(d, math.Float64bits(v))
	m["f64"] = d
	return m
}

func indexAll(buf, pat []byte) []int {
	var out []int
	for off := 0; ; {
		i := indexFrom(buf, pat, off)
		if i < 0 {
			return out
		}
		out = append(out, i)
		off = i + 1
	}
}

func indexFrom(buf, pat []byte, off int) int {
	if off >= len(buf) {
		return -1
	}
	for i := off; i+len(pat) <= len(buf); i++ {
		if buf[i] == pat[0] && string(buf[i:i+len(pat)]) == string(pat) {
			return i
		}
	}
	return -1
}

// scanLatency prints every place where 1802 and 901 (same encoding) lie within
// 64 bytes of each other, with 46875 nearby if present.
func scanLatency(pid int) {
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mem.Close()
	for _, r := range rwRegions(pid) {
		size := r.end - r.start
		if size > 64<<20 {
			continue
		}
		buf := make([]byte, size)
		if _, err := mem.ReadAt(buf, int64(r.start)); err != nil {
			continue
		}
		for enc, p1802 := range encodings(1802) {
			p901 := encodings(901)[enc]
			p46875 := encodings(46875)[enc]
			for _, i := range indexAll(buf, p1802) {
				lo, hi := i-64, i+64
				if lo < 0 {
					lo = 0
				}
				if hi > len(buf) {
					hi = len(buf)
				}
				win := buf[lo:hi]
				if indexFrom(win, p901, 0) < 0 {
					continue
				}
				near := indexFrom(win, p46875, 0) >= 0
				fmt.Printf("%s at %#x (region %#x) 46875 near=%v\n  % x\n", enc, r.start+uint64(i), r.start, near, win)
			}
		}
	}
}

// patchLatency writes ms over the value at addr (encoding enc).
func patchLatency(pid int, addr uint64, enc string, ms float64) error {
	mem, err := os.OpenFile(fmt.Sprintf("/proc/%d/mem", pid), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer mem.Close()
	_, err = mem.WriteAt(encodings(ms)[enc], int64(addr))
	return err
}

// latencySig is the run of config values right before wifiBufferTarget:
// wifiAttenuation 0.398107 (f32), auxAttenuation 1.0 (f32).
var latencySig = []byte{0xae, 0xd4, 0xcb, 0x3e, 0x00, 0x00, 0x80, 0x3f}

// findLatency returns the addresses of every wifiBufferTarget copy whose
// current value is cur (i32, followed by auxBufferTarget 901).
func findLatency(pid int, cur uint32) []uint64 {
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil
	}
	defer mem.Close()
	// Read in small pieces: the box has a few MB of RAM to spare, not the
	// size of APServer's heap.
	const chunk = 64 << 10
	const tail = 16 // signature + value + 901, carried over between pieces
	buf := make([]byte, chunk+tail)
	var out []uint64
	for _, r := range rwRegions(pid) {
		for off := r.start; off < r.end; off += chunk {
			n := uint64(chunk + tail)
			if off+n > r.end {
				n = r.end - off
			}
			got, err := mem.ReadAt(buf[:n], int64(off))
			if err != nil && got == 0 {
				break
			}
			b := buf[:got]
			for _, i := range indexAll(b, latencySig) {
				if uint64(i) >= chunk {
					continue // seen again at the start of the next piece
				}
				j := i + len(latencySig)
				if j+8 > len(b) {
					continue
				}
				if binary.LittleEndian.Uint32(b[j:]) == cur && binary.LittleEndian.Uint32(b[j+4:]) == 901 {
					out = append(out, off+uint64(j))
				}
			}
		}
	}
	return out
}

// setLatency writes ms to every address.
func setLatency(pid int, addrs []uint64, ms uint32) error {
	mem, err := os.OpenFile(fmt.Sprintf("/proc/%d/mem", pid), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer mem.Close()
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, ms)
	for _, a := range addrs {
		if _, err := mem.WriteAt(b, int64(a)); err != nil {
			return err
		}
	}
	return nil
}
