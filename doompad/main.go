// doompad: runs DOOM on the SoundTouch Portable's OLED and turns the Bose IR
// remote into its controller. Throwaway spike, RAM only.
//
// Panel: BoseApp's DirectFB flip thread is held (PTRACE_SEIZE+INTERRUPT), so
// nothing but us writes /dev/fb0 (128x100, 8 bpp, 16 grey levels).
// Keys: BoseApp's DevHelperThread is traced syscall by syscall. It logs every
// remote key as "IR Key event: Key()=N, State()=S" through sendmsg; that line
// gives us the key. While a key is being handled, every output syscall of the
// thread is turned into getpid (the log line itself and whatever hands the key
// on), so neither Bose nor STR sees the key. Power ends the game.
//
// Deliberately no net/http or crypto import: since Go 1.26 crypto links a
// 32 MB BSS buffer, which this box's kernel refuses to exec when memory is
// short.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ptraceSeize      = 0x4206
	ptraceInterrupt  = 0x4207
	ptraceDetach     = 17
	ptraceSetOptions = 0x4200
	ptraceSetSyscall = 23
	optTraceSysGood  = 1
	wAll             = 0x40000000
	armR7            = 7
	armIP            = 12

	W, H       = 128, 100
	frameSize  = W * H
	doomW      = 128
	doomH      = 80
	doomFrame  = doomW * doomH
	doomTop    = (H - doomH) / 2
	sysGetpid  = 20
	keyPowerNr = 8
)

// Doom key codes (doomkeys.h).
const (
	dkRight  = 0xae
	dkLeft   = 0xac
	dkUp     = 0xad
	dkDown   = 0xaf
	dkStrafL = 0xa0
	dkStrafR = 0xa1
	dkUse    = 0xa2
	dkFire   = 0xa3
	dkTab    = 9
)

// remote key (BoseApp KEY_VAL order) -> doom key. The remote is held
// sideways: preset 1 left, 5 forward, 4 back, volume up right; skip and the
// thumbs do the rest. Every other key is swallowed and does nothing.
var keyMap = map[int]byte{
	12: dkLeft,  // preset 1
	16: dkUp,    // preset 5
	15: dkDown,  // preset 4
	10: dkRight, // volume up
	3:  dkFire,  // prev: fire
	4:  dkUse,   // skip: doors, switches
	5:  dkTab,   // thumbs up: automap
	6:  'n',     // thumbs down: next weapon
}

func hold(tid int) error {
	if _, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptraceSeize, uintptr(tid), 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("seize %d: %v", tid, e)
	}
	if _, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptraceInterrupt, uintptr(tid), 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("interrupt %d: %v", tid, e)
	}
	_, err := waitStop(tid)
	return err
}

func release(tid int) { syscall.Syscall6(syscall.SYS_PTRACE, ptraceDetach, uintptr(tid), 0, 0, 0, 0) }

func waitStop(tid int) (syscall.WaitStatus, error) {
	var ws syscall.WaitStatus
	_, err := syscall.Wait4(tid, &ws, wAll, nil)
	return ws, err
}

func findThread(pid int, comm string) int {
	dir := fmt.Sprintf("/proc/%d/task", pid)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, err := os.ReadFile(dir + "/" + e.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(b)) == comm {
			n, _ := strconv.Atoi(e.Name())
			return n
		}
	}
	return 0
}

func findPid(name string) int {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(b)) == name {
			return pid
		}
	}
	return 0
}

// findFd returns the fd number pid holds open on path, 0 if none.
func findFd(pid int, path string) uint32 {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if l, err := os.Readlink(dir + "/" + e.Name()); err == nil && l == path {
			n, _ := strconv.Atoi(e.Name())
			return uint32(n)
		}
	}
	return 0
}

func enableKeyLog() {
	c, err := net.DialTimeout("tcp", "127.0.0.1:17000", 2*time.Second)
	if err != nil {
		fmt.Println("tap:", err)
		return
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte("loglevel IrDevice on 4\r\n"))
	buf := make([]byte, 256)
	c.Read(buf)
}

type keyEvent struct{ key, state int }

var keyMarker = []byte("IR Key event: Key()=")

func parseKeyLine(b []byte) (key, state int, ok bool) {
	i := bytes.Index(b, keyMarker)
	if i < 0 {
		return 0, 0, false
	}
	rest := b[i+len(keyMarker):]
	num := func(r []byte) (int, []byte, bool) {
		j := 0
		for j < len(r) && r[j] >= '0' && r[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(string(r[:j]))
		return n, r[j:], err == nil
	}
	key, rest, ok = num(rest)
	if !ok {
		return 0, 0, false
	}
	k := bytes.Index(rest, []byte("State()="))
	if k < 0 {
		return key, 0, true
	}
	state, _, ok = num(rest[k+len("State()="):])
	return key, state, ok
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// output syscalls neutralised while a key is handled
var outputNr = map[uint32]bool{4: true, 146: true, 289: true, 290: true, 296: true}

// syscalls that mean the thread went back to waiting: the key is handled
var waitNr = map[uint32]bool{142: true, 168: true, 252: true, 336: true, 335: true, 3: true}

// irCode maps the byte BoseApp reads from /dev/ir to the key (KEY_VAL
// order). Learnt on the Portable's remote, 2026-10-18; a release reads 00.
var irCode = map[byte]int{
	0x1f: 12, // preset 1
	0x2f: 16, // preset 5
	0xaf: 15, // preset 4
	0x3f: 10, // volume up
	0x67: 4,  // skip
	0xaa: 5,  // thumbs up
	0xda: 6,  // thumbs down
	0xcd: 8,  // power
	0xe7: 3,  // prev
	0x27: 24, // play/pause
}

type tracer struct {
	irFd     uint32 // BoseApp's fd on /dev/ir; reads on it are taken away
	irLast   int    // key of the last IR press, for the release byte
	tid      int
	learn    io.Writer // logs every short read of the thread with its bytes
	swallow  bool
	logf     io.Writer
	peekBuf  []byte
	armed    int // remaining syscalls in the swallow window
	fakeRet  uint32
	faked    bool
	window   int
	nr       uint32
	a0, a1   uint32
	a2       uint32
	entryArm bool
}

func (t *tracer) peek(addr uint32, n int) []byte {
	if n > 512 {
		n = 512
	}
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	if _, err := syscall.PtracePeekData(t.tid, uintptr(addr), b); err != nil {
		return nil
	}
	return b
}

// iovTotal returns the summed length and the first 512 bytes of an iovec.
func (t *tracer) iov(iov, n uint32) (uint32, []byte) {
	if n == 0 || n > 16 {
		return 0, nil
	}
	vec := t.peek(iov, int(8*n))
	if vec == nil {
		return 0, nil
	}
	var total uint32
	var out []byte
	for i := uint32(0); i < n; i++ {
		l := le32(vec[8*i+4:])
		total += l
		if len(out) < 512 {
			if part := t.peek(le32(vec[8*i:]), int(l)); part != nil {
				out = append(out, part...)
			}
		}
	}
	return total, out
}

func (t *tracer) run(pid int, quit <-chan struct{}, out chan<- keyEvent) error {
	if _, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptraceSetOptions, uintptr(t.tid), 0, optTraceSysGood, 0, 0); e != 0 {
		return fmt.Errorf("setoptions: %v", e)
	}
	go func() {
		<-quit
		syscall.Tgkill(pid, t.tid, syscall.SIGWINCH)
	}()
	quitting := func() bool {
		select {
		case <-quit:
			return true
		default:
			return false
		}
	}
	deliver := 0
	for {
		if err := syscall.PtraceSyscall(t.tid, deliver); err != nil {
			return fmt.Errorf("ptrace syscall: %v", err)
		}
		deliver = 0
		ws, err := waitStop(t.tid)
		if err != nil {
			return err
		}
		if ws.Exited() || ws.Signaled() {
			return fmt.Errorf("thread gone")
		}
		if !ws.Stopped() {
			continue
		}
		sig := ws.StopSignal()
		if sig != syscall.SIGTRAP|0x80 {
			if sig == syscall.SIGWINCH && quitting() {
				return nil
			}
			if uint32(ws)>>16 == 0 && sig != syscall.SIGTRAP {
				deliver = int(sig)
			}
			continue
		}
		var r syscall.PtraceRegs
		if err := syscall.PtraceGetRegs(t.tid, &r); err != nil {
			return err
		}
		if r.Uregs[armIP] == 0 { // entry
			t.nr, t.a0, t.a1, t.a2 = r.Uregs[armR7], r.Uregs[0], r.Uregs[1], r.Uregs[2]
			t.faked = false
			var msg []byte
			var length uint32
			switch t.nr {
			case 4, 289, 290:
				length = t.a2
				msg = t.peek(t.a1, int(t.a2))
			case 146:
				length, msg = t.iov(t.a1, t.a2)
			case 296:
				if h := t.peek(t.a1, 16); h != nil {
					length, msg = t.iov(le32(h[8:]), le32(h[12:]))
				}
			}
			isKey := false
			if msg != nil {
				if k, s, ok := parseKeyLine(msg); ok {
					isKey = true
					select {
					case out <- keyEvent{k, s}:
					default:
					}
					if t.swallow && t.irFd == 0 {
						t.armed = 60 // fallback when /dev/ir was not found
						t.window++
					}
				}
			}
			if t.armed > 0 {
				if waitNr[t.nr] && !isKey {
					t.armed = 0
				} else {
					t.armed--
					if t.logf != nil {
						fmt.Fprintf(t.logf, "w%d > %d(%d, %#x, %d) %q\n", t.window, t.nr, t.a0, t.a1, t.a2, trim(msg))
					}
					if outputNr[t.nr] {
						if _, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptraceSetSyscall, uintptr(t.tid), 0, sysGetpid, 0, 0); e == 0 {
							t.faked, t.fakeRet = true, length
						}
					}
				}
			}
			continue
		}
		// exit
		if t.swallow && t.irFd != 0 && t.nr == 3 && t.a0 == t.irFd {
			if ret := int32(r.Uregs[0]); ret > 0 {
				for _, c := range t.peek(t.a1, int(ret)) {
					if c == 0 {
						if t.irLast >= 0 {
							select {
							case out <- keyEvent{t.irLast, 1}:
							default:
							}
						}
						t.irLast = -1
						continue
					}
					k, ok := irCode[c]
					if !ok {
						k = 1000 + int(c) // unknown key: swallowed, no action
						fmt.Printf("ir: unknown code %02x\n", c)
					}
					t.irLast = k
					select {
					case out <- keyEvent{k, 0}:
					default:
					}
				}
				// BoseApp never sees the key: the read reports "nothing yet".
				r.Uregs[0] = uint32(0xfffffff5) // -EAGAIN
				syscall.PtraceSetRegs(t.tid, &r)
			}
			continue
		}
		if t.learn != nil && t.nr == 3 {
			if ret := int32(r.Uregs[0]); ret > 0 && ret <= 64 {
				fmt.Fprintf(t.learn, "%s read fd=%d % x\n", time.Now().Format("15:04:05.000"), t.a0, t.peek(t.a1, int(ret)))
			}
		}
		if t.learn != nil && t.nr == 296 {
			if h := t.peek(t.a1, 16); h != nil {
				if _, m := t.iov(le32(h[8:]), le32(h[12:])); m != nil {
					if k, st, ok := parseKeyLine(m); ok {
						fmt.Fprintf(t.learn, "%s key %d state %d\n", time.Now().Format("15:04:05.000"), k, st)
					}
				}
			}
		}
		if t.faked {
			r.Uregs[0] = t.fakeRet
			syscall.PtraceSetRegs(t.tid, &r)
			t.faked = false
		}
	}
}

func trim(b []byte) []byte {
	if len(b) > 90 {
		return b[:90]
	}
	return b
}

func main() {
	doomBin := flag.String("doom", "/dev/shm/doom/doom-str", "doom binary")
	wad := flag.String("iwad", "/dev/shm/doom/doom1.wad", "IWAD")
	swallow := flag.Bool("swallow", true, "keep keys away from Bose and STR while playing")
	logPath := flag.String("log", "", "write the swallowed syscalls here")
	gamma := flag.Float64("gamma", 0.55, "brightness curve for the 16 grey levels")
	skill := flag.String("skill", "2", "doom skill 1..5")
	sound := flag.Bool("sound", true, "stream the game sound to the speaker")
	prebuf := flag.Duration("prebuf", 250*time.Millisecond, "silence sent before the paced stream")
	drain := flag.Duration("drain", 0, "pause after the prebuffer so the speaker's buffer runs down")
	vol := flag.Int("vol", 20, "speaker volume while playing")
	learnSecs := flag.Int("learn", 0, "only print raw key reads and key lines for N seconds, no game")
	irExtra := flag.String("ir", "", "extra IR codes, e.g. 4b=3,4c=24 (hex code = key number)")
	latMs := flag.Int("lat", 350, "speaker buffer target in ms while playing (stock 1802), 0 = leave it")
	lowDetail := flag.Bool("lowdetail", false, "render every other column (Doom's F5 low detail)")
	title := flag.Bool("title", true, "start on Doom's title screen with its music; skip starts a new game")
	latWhich := flag.Int("latwhich", 0, "0 = every copy, 1 = only the first, 2 = only the second")
	scanLat := flag.Bool("scanlat", false, "look for APServer's wifi buffer target in its memory and exit")
	flag.Parse()
	runtime.LockOSThread()
	if *scanLat {
		scanLatency(findPid("APServer"))
		return
	}
	if *learnSecs > 0 {
		pid := findPid("BoseApp")
		enableKeyLog()
		tid := findThread(pid, "DevHelperThread")
		if err := hold(tid); err != nil {
			fmt.Println(err)
			return
		}
		defer release(tid)
		quit := make(chan struct{})
		time.AfterFunc(time.Duration(*learnSecs)*time.Second, func() { close(quit) })
		tr := &tracer{tid: tid, learn: os.Stdout}
		if err := tr.run(pid, quit, make(chan keyEvent, 64)); err != nil {
			fmt.Println(err)
		}
		return
	}

	pid := findPid("BoseApp")
	if pid == 0 {
		fmt.Println("BoseApp not found")
		os.Exit(1)
	}
	var lvl [256]byte
	for i := range lvl {
		lvl[i] = byte(math.Round(15*math.Pow(float64(i)/255, *gamma))) * 0x11
	}

	quit := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(quit) }) }
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM) // not SIGHUP: the launcher starts it detached
	go func() { <-sig; stop() }()

	var held []int
	defer func() {
		for _, t := range held {
			release(t)
		}
		fmt.Println("released", held)
	}()
	flip := findThread(pid, "DirectFBFlipTas")
	if err := hold(flip); err != nil {
		fmt.Println("flip thread:", err)
		return
	}
	held = append(held, flip)
	enableKeyLog()
	keyTid := findThread(pid, "DevHelperThread")
	if err := hold(keyTid); err != nil {
		fmt.Println("key thread:", err)
		return
	}
	held = append(held, keyTid)

	f, err := os.OpenFile("/dev/fb0", os.O_RDWR, 0)
	if err != nil {
		fmt.Println(err)
		return
	}
	fbm, err := syscall.Mmap(int(f.Fd()), 0, 16384, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Println(err)
		return
	}
	fb := fbm[:frameSize]
	for i := range fb {
		fb[i] = 0
	}

	pr, pw, _ := os.Pipe()
	args := []string{"-iwad", *wad}
	if !*title {
		args = append(args, "-warp", "1", "1", "-skill", *skill)
	}
	if !*sound {
		args = append(args, "-nosound")
	}
	cmd := exec.Command(*doomBin, args...)
	cmd.Dir = "/dev/shm/doom"
	cmd.Env = append(os.Environ(), "HOME=/dev/shm/doom")
	if *lowDetail {
		cmd.Env = append(cmd.Env, "DOOM_LOWDETAIL=1")
	}
	ar, aw, _ := os.Pipe()
	cmd.ExtraFiles = []*os.File{pw, aw}
	keyIn, _ := cmd.StdinPipe()
	logOut, _ := os.Create("/dev/shm/doom/doom.out")
	cmd.Stdout, cmd.Stderr = logOut, logOut
	if err := cmd.Start(); err != nil {
		fmt.Println("doom:", err)
		return
	}
	pw.Close()
	aw.Close()
	var undo []prioChange
	renice(os.Getpid(), -5, &undo)
	renice(cmd.Process.Pid, -5, &undo)
	renice(findPid("streborn-armv7l"), 10, &undo)
	renice(findPid("go-librespot"), 19, &undo)
	defer restorePrio(undo)
	if *sound && *latMs > 0 {
		ap := findPid("APServer")
		addrs := findLatency(ap, 1802)
		if *latWhich > 0 && *latWhich <= len(addrs) {
			addrs = addrs[*latWhich-1 : *latWhich]
		}
		if len(addrs) > 0 {
			if err := setLatency(ap, addrs, uint32(*latMs)); err != nil {
				fmt.Println("latency:", err)
			} else {
				fmt.Printf("latency: buffer target %d ms at %d places\n", *latMs, len(addrs))
				defer func() {
					setLatency(ap, addrs, 1802)
					fmt.Println("latency: back to 1802 ms")
				}()
			}
		} else {
			fmt.Println("latency: buffer target not found, left as it is")
		}
	}
	if *sound {
		ring := &pcmRing{}
		go readPCM(ar, ring, inRate*250/1000)
		if err := serveSound(ring, *prebuf, *drain, quit); err != nil {
			fmt.Println("sound:", err)
		} else {
			stopSound := startSound(*vol)
			defer stopSound()
		}
	}
	go func() { cmd.Wait(); stop() }()
	defer func() { cmd.Process.Kill() }()

	// frames
	go func() {
		buf := make([]byte, doomFrame)
		frames, since := 0, time.Now()
		for {
			if frames++; time.Since(since) >= 10*time.Second {
				fmt.Printf("fps %.1f\n", float64(frames)/time.Since(since).Seconds())
				frames, since = 0, time.Now()
			}
			if _, err := io.ReadFull(pr, buf); err != nil {
				stop()
				return
			}
			o := doomTop * W
			for i, v := range buf {
				fb[o+i] = lvl[v]
			}
		}
	}()

	// keys
	keys := make(chan keyEvent, 64)
	go func() {
		down := map[byte]bool{}
		send := func(p byte, k byte) { keyIn.Write([]byte{p, k}) }
		tap := func(k byte) {
			send(1, k)
			time.Sleep(80 * time.Millisecond)
			send(0, k)
			time.Sleep(250 * time.Millisecond)
		}
		inGame := !*title
		for {
			select {
			case <-quit:
				return
			case ev := <-keys:
				if ev.key == keyPowerNr {
					if ev.state == 0 {
						fmt.Println("power: leaving the game")
						stop()
					}
					continue
				}
				if !inGame {
					// title screen or attract demos: skip starts a new game
					// through the menu, as a player would (New Game,
					// Knee-Deep in the Dead, Hurt me plenty)
					if ev.key == 4 && ev.state == 0 {
						inGame = true
						go func() {
							for _, k := range []byte{27, 13, 13, 13} {
								tap(k)
							}
						}()
					}
					continue
				}
				dk, ok := keyMap[ev.key]
				if !ok {
					continue
				}
				switch ev.state {
				case 0:
					if !down[dk] {
						down[dk] = true
						send(1, dk)
					}
				case 1:
					if down[dk] {
						down[dk] = false
						send(0, dk)
					}
				}
			}
		}
	}()

	var logw io.Writer
	if *logPath != "" {
		if lf, err := os.Create(*logPath); err == nil {
			defer lf.Close()
			logw = lf
		}
	}
	for _, kv := range strings.Split(*irExtra, ",") {
		var code, key int
		if _, err := fmt.Sscanf(kv, "%x=%d", &code, &key); err == nil {
			irCode[byte(code)] = key
		}
	}
	tr := &tracer{tid: keyTid, swallow: *swallow, logf: logw, irFd: findFd(pid, "/dev/ir"), irLast: -1}
	fmt.Println("ir fd", tr.irFd)
	if err := tr.run(pid, quit, keys); err != nil {
		fmt.Println("keys:", err)
	}
	for i := range fb {
		fb[i] = 0
	}
}
