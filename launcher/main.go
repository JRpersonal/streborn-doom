// str-doom puts DOOM on a SoundTouch speaker that runs STR, plays it, and
// leaves nothing behind: everything goes into the speaker's RAM, and a restart
// of the speaker removes it.
//
//	str-doom <speaker-ip>          play (title screen, skip starts a game)
//	str-doom stop <speaker-ip>     end a running game
//	str-doom learn <speaker-ip>    print the codes of your remote's keys
//
// Started without any arguments (a double-click on Windows), it asks for the
// speaker's address and waits for Enter before it exits, so the window stays
// open long enough to read.
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var version = "dev"

//go:embed bin/doom-str
var doomBin []byte

//go:embed bin/doompad
var padBin []byte

const remoteDir = "/dev/shm/doom"

func usage() {
	fmt.Fprintf(os.Stderr, `str-doom %s: DOOM on a SoundTouch speaker running STR

  str-doom [flags] <speaker-ip>        start DOOM
  str-doom stop <speaker-ip>           end a running game
  str-doom learn <speaker-ip>          show the codes of your remote's keys

Flags:
`, version)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Remote, held sideways: 1 turn left, 5 forward, 4 back, volume up turn right,
skip back fire, skip use (doors), thumbs up map, thumbs down next weapon.
Power ends the game. Everything runs from the speaker's RAM.
`)
}

func main() {
	wadPath := flag.String("wad", "", "your own DOOM .wad (default: the free shareware DOOM1.WAD, downloaded once)")
	vol := flag.Int("vol", 20, "speaker volume while playing")
	lat := flag.Int("lat", 350, "sound delay target in ms (the speaker's own is 1802; below about 300 it goes silent)")
	ir := flag.String("ir", "", "extra remote codes from 'learn', e.g. e7=3,27=24")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	// No arguments at all: most likely a double-click. Ask for the address
	// and keep the window open at the end.
	interactive := len(os.Args) == 1
	stdin := bufio.NewReader(os.Stdin)
	exit := func(code int) {
		if interactive {
			waitForEnter(stdin, os.Stdout)
		}
		os.Exit(code)
	}
	if interactive {
		host, err := askHost(stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "str-doom:", err)
			exit(1)
		}
		args = []string{host}
	}

	cmd := "play"
	if len(args) == 2 && (args[0] == "stop" || args[0] == "learn") {
		cmd, args = args[0], args[1:]
	}
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	host := args[0]
	if err := run(cmd, host, *wadPath, *vol, *lat, *ir); err != nil {
		fmt.Fprintln(os.Stderr, "str-doom:", err)
		exit(1)
	}
	exit(0)
}

// askHost asks for the speaker's address until it gets a non-empty line.
func askHost(r *bufio.Reader, w io.Writer) (string, error) {
	for {
		fmt.Fprint(w, "Speaker IP address: ")
		line, err := r.ReadString('\n')
		if host := strings.TrimSpace(line); host != "" {
			return host, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("no speaker address given")
			}
			return "", err
		}
	}
}

// waitForEnter keeps a double-clicked window open until Enter is pressed.
func waitForEnter(r *bufio.Reader, w io.Writer) {
	fmt.Fprint(w, "\nPress Enter to close.")
	_, _ = r.ReadString('\n')
}

func run(cmd, host, wadPath string, vol, lat int, ir string) error {
	info, err := findAgent(host)
	if err != nil {
		return err
	}
	fmt.Printf("Speaker: %s (%s), STR %s\n", info.Name, info.Model, info.Version)
	if !strings.Contains(info.Model, "Portable") {
		fmt.Println("Note: only the SoundTouch Portable is tested. The 20 and 30 have the same display; tell me how it goes.")
	}
	sh, err := connect(host, info)
	if err != nil {
		return err
	}
	defer sh.Close()

	switch cmd {
	case "stop":
		_, err := sh.run("killall doompad 2>/dev/null; sleep 2; killall doom-str 2>/dev/null; echo stopped", nil)
		fmt.Println("Stopped.")
		return err
	case "learn":
		if err := upload(sh, false, wadPath); err != nil {
			return err
		}
		fmt.Println("Press each key of your remote once within 60 seconds (the speaker still reacts to them)...")
		out, err := sh.run(remoteDir+"/doompad -learn 60 2>&1 | grep -B1 'key ' | grep -E 'fd=|key '", nil)
		fmt.Println(learnTable(out))
		return err
	}

	if err := upload(sh, true, wadPath); err != nil {
		return err
	}
	wad := remoteDir + "/doom1.wad"
	if wadPath != "" {
		wad = remoteDir + "/game.wad"
	}
	start := fmt.Sprintf("cd %s && killall doompad 2>/dev/null; sleep 1; "+
		"(nohup ./doompad -iwad %s -vol %d -lat %d -ir '%s' </dev/null >pad.out 2>&1 &) ; sleep 3; head -3 pad.out",
		remoteDir, wad, vol, lat, ir)
	out, err := sh.run(start, nil)
	if err != nil {
		return err
	}
	fmt.Print(out)
	fmt.Println(`
DOOM is running. Look at the speaker's display.
  skip           start a new game from the title screen
  1 / volume up  turn left / right      5 / 4   forward / back
  skip back      fire                   skip    open doors, switches
  thumbs up      map                    thumbs down  next weapon
  power          end the game
Sound comes from the speaker a moment later than the picture; that is the speaker's own buffer.`)
	return nil
}

// upload copies the speaker side into its RAM.
func upload(sh *shell, withWAD bool, wadPath string) error {
	if _, err := sh.run("mkdir -p "+remoteDir, nil); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		exec bool
	}{{"doom-str", doomBin, true}, {"doompad", padBin, true}}
	if withWAD {
		var wad []byte
		var err error
		name := "doom1.wad"
		if wadPath != "" {
			wad, err = os.ReadFile(wadPath)
			name = "game.wad"
		} else {
			wad, err = sharewareWAD()
		}
		if err != nil {
			return err
		}
		files = append(files, struct {
			name string
			data []byte
			exec bool
		}{name, wad, false})
	}
	for _, f := range files {
		fmt.Printf("Copying %s (%d KB) to the speaker's RAM...\n", f.name, len(f.data)/1024)
		mode := "644"
		if f.exec {
			mode = "755"
		}
		cmd := fmt.Sprintf("cat > %s/%s.part && mv %s/%s.part %s/%s && chmod %s %s/%s",
			remoteDir, f.name, remoteDir, f.name, remoteDir, f.name, mode, remoteDir, f.name)
		if _, err := sh.run(cmd, bytes.NewReader(f.data)); err != nil {
			return fmt.Errorf("copy %s: %w", f.name, err)
		}
	}
	return nil
}

// learnTable turns doompad's learn output into "code = key" lines.
func learnTable(out string) string {
	var b strings.Builder
	var code string
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "fd="); i >= 0 {
			f := strings.Fields(l[i:])
			if len(f) >= 2 {
				code = f[1]
			}
			continue
		}
		if i := strings.Index(l, "key "); i >= 0 && code != "" && code != "00" {
			var key, state int
			fmt.Sscanf(l[i:], "key %d state %d", &key, &state)
			if state == 0 {
				fmt.Fprintf(&b, "code %s = key %d\n", code, key)
			}
			code = ""
		}
	}
	if b.Len() == 0 {
		return "No keys seen."
	}
	return b.String() + "Pass the ones the game needs with -ir code=key,code=key."
}
