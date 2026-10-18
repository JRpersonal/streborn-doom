package main

// Game sound on the speaker with as little delay as the speaker allows.
//
// Doom mixes its effects at 11025 Hz stereo and writes them to a pipe as they
// become due (i_strsound.c). Here they are upsampled to 44.1 kHz and served as
// an endless WAV that the speaker plays through its own UPnP renderer on
// loopback. The earlier games were seconds late because their server raced
// ahead of real time and the speaker happily buffered all of it. Here nothing
// is ever sent ahead: after a small fixed prebuffer the stream is paced by the
// wall clock, the socket send buffer is kept tiny, and sound that arrives
// late is dropped instead of queued.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	outRate   = 44100 // the box buffers bytes, so more bytes per second means less delay
	inRate    = 11025
	audioPort = 8097
)

// pcmRing holds Doom's newest samples (stereo frames at 11025 Hz).
type pcmRing struct {
	mu  sync.Mutex
	buf []int16 // interleaved L R
}

func (p *pcmRing) push(b []int16, maxFrames int) {
	p.mu.Lock()
	p.buf = append(p.buf, b...)
	if over := len(p.buf)/2 - maxFrames; over > 0 {
		p.buf = p.buf[2*over:] // late sound is dropped, never queued
	}
	p.mu.Unlock()
}

// take returns up to n frames, padding with silence.
func (p *pcmRing) take(n int, out []int16) {
	p.mu.Lock()
	k := len(p.buf) / 2
	if k > n {
		k = n
	}
	copy(out, p.buf[:2*k])
	p.buf = p.buf[2*k:]
	p.mu.Unlock()
	for i := 2 * k; i < 2*n; i++ {
		out[i] = 0
	}
}

func readPCM(r io.Reader, ring *pcmRing, maxFrames int) {
	b := make([]byte, 4096)
	s := make([]int16, 2048)
	have := 0 // bytes of an incomplete frame carried over to the next read
	for {
		n, err := r.Read(b[have:])
		if err != nil {
			return
		}
		n += have
		whole := n - n%4
		for i := 0; i < whole/2; i++ {
			s[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		}
		ring.push(s[:whole/2], maxFrames)
		have = copy(b, b[whole:n])
	}
}

func wavHeader() []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 0xFFFFFFFF)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 2)
	binary.LittleEndian.PutUint32(h[24:], outRate)
	binary.LittleEndian.PutUint32(h[28:], outRate*4)
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], 0xFFFFFFFF)
	return h
}

// serveSound answers the speaker's GET with the paced stream.
func serveSound(ring *pcmRing, prebuf, drainFor time.Duration, quit <-chan struct{}) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", audioPort))
	if err != nil {
		return err
	}
	go func() { <-quit; ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go streamTo(c.(*net.TCPConn), ring, prebuf, drainFor, quit)
		}
	}()
	return nil
}

func streamTo(c *net.TCPConn, ring *pcmRing, prebuf, drainFor time.Duration, quit <-chan struct{}) {
	defer c.Close()
	req, _ := bufio.NewReader(c).ReadString('\n')
	fmt.Printf("sound: speaker connected %q\n", strings.TrimSpace(req))
	c.SetWriteBuffer(4096)
	c.SetNoDelay(true)
	hdr := "HTTP/1.0 200 OK\r\nContent-Type: audio/wav\r\nCache-Control: no-cache\r\n\r\n"
	if strings.HasPrefix(req, "HEAD") {
		c.Write([]byte(hdr))
		return
	}
	if _, err := c.Write(append([]byte(hdr), wavHeader()...)); err != nil {
		return
	}
	// the prebuffer: silence the renderer needs before it starts at all
	if pre := int(prebuf.Seconds() * outRate); pre > 0 {
		if _, err := c.Write(make([]byte, 4*pre)); err != nil {
			return
		}
	}
	// Then stay quiet while the speaker plays that silence down: its decode
	// buffer starts out nearly full and would otherwise stay that far ahead
	// for good.
	if drainFor > 0 {
		select {
		case <-quit:
			return
		case <-time.After(drainFor):
		}
	}
	const tick = 10 * time.Millisecond
	up := newUpsampler()
	in := make([]int16, 2*inRate/50)
	out := make([]byte, 4*outRate/50)
	start := time.Now()
	sent := 0 // output frames after the prebuffer
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
		}
		due := int(time.Since(start).Seconds()*outRate) - sent
		due -= due % upFactor
		if due <= 0 {
			continue
		}
		if due > outRate/50 {
			due = outRate / 50
		}
		n := due / upFactor // input frames
		ring.take(n, in)
		o := 0
		for i := 0; i < n; i++ {
			up.push(in[2*i], in[2*i+1])
			for k := 0; k < upFactor; k++ {
				lv, rv := up.phase(k)
				binary.LittleEndian.PutUint16(out[o:], uint16(lv))
				binary.LittleEndian.PutUint16(out[o+2:], uint16(rv))
				o += 4
			}
		}
		if _, err := c.Write(out[:o]); err != nil {
			fmt.Println("sound: speaker went away:", err)
			return
		}
		sent += due
	}
}

// httpDo sends one plain HTTP/1.0 request to the box and returns the status.
func httpDo(addr, method, path, ctype, soapAction, body string) (int, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.0\r\nHost: %s\r\nContent-Length: %d\r\n", method, path, addr, len(body))
	if ctype != "" {
		fmt.Fprintf(&b, "Content-Type: %s\r\n", ctype)
	}
	if soapAction != "" {
		fmt.Fprintf(&b, "SOAPACTION: %s\r\n", soapAction)
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	if _, err := c.Write([]byte(b.String())); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return 0, err
	}
	var code int
	fmt.Sscanf(line, "HTTP/%s %d", new(string), &code)
	io.Copy(io.Discard, c)
	return code, nil
}

func soap(action, body string) error {
	env := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` +
		action + ` xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID>` + body + `</u:` + action + `></s:Body></s:Envelope>`
	code, err := httpDo("127.0.0.1:8091", "POST", "/AVTransport/Control", `text/xml; charset="utf-8"`,
		`"urn:schemas-upnp-org:service:AVTransport:1#`+action+`"`, env)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("%s: status %d", action, code)
	}
	return nil
}

func boseHTTP(method, path, body string) {
	httpDo("127.0.0.1:8090", method, path, "", "", body)
}

// startSound tells the speaker to play the stream at volume vol. The returned
// func stops it and puts the speaker back into standby.
func startSound(vol int) func() {
	uri := fmt.Sprintf("http://127.0.0.1:%d/doom.wav", audioPort)
	meta := `&lt;DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"&gt;&lt;item id="1" parentID="0" restricted="1"&gt;&lt;dc:title&gt;DOOM&lt;/dc:title&gt;&lt;upnp:class&gt;object.item.audioItem.audioBroadcast&lt;/upnp:class&gt;&lt;res protocolInfo="http-get:*:audio/wav:*"&gt;` + uri + `&lt;/res&gt;&lt;/item&gt;&lt;/DIDL-Lite&gt;`
	v := fmt.Sprintf("<volume>%d</volume>", vol)
	boseHTTP("POST", "/volume", v)
	if err := soap("SetAVTransportURI", "<CurrentURI>"+uri+"</CurrentURI><CurrentURIMetaData>"+meta+"</CurrentURIMetaData>"); err != nil {
		fmt.Println("sound:", err)
	}
	if err := soap("Play", "<Speed>1</Speed>"); err != nil {
		fmt.Println("sound:", err)
	}
	// waking up restores the last volume, so insist for a few seconds
	go func() {
		for i := 0; i < 16; i++ {
			boseHTTP("POST", "/volume", v)
			time.Sleep(500 * time.Millisecond)
		}
	}()
	return func() {
		_ = soap("Stop", "")
		boseHTTP("GET", "/standby", "")
	}
}

// Polyphase windowed-sinc interpolation 11025 -> 44100 Hz. Linear
// interpolation left the 11 kHz images in, which is most of what made the
// effects sound harsh. Integer only at run time; the box has no FPU worth
// using.
const (
	upFactor = outRate / inRate
	upTaps   = 8 // per phase, 32 in all
)

type upsampler struct {
	coef [upFactor][upTaps]int32 // Q15
	hl   [upTaps]int32
	hr   [upTaps]int32
}

func newUpsampler() *upsampler {
	u := &upsampler{}
	n := upFactor * upTaps
	for p := 0; p < upFactor; p++ {
		var raw [upTaps]float64
		sum := 0.0
		for t := 0; t < upTaps; t++ {
			i := t*upFactor + p
			// centred on the input sample, zeros on every other input sample:
			// a true interpolator, the input samples come out unchanged
			x := float64(i-n/2) / upFactor
			s := 1.0
			if x != 0 {
				s = math.Sin(math.Pi*x) / (math.Pi * x)
			}
			w := 0.42 + 0.5*math.Cos(math.Pi*float64(i-n/2)/float64(n/2)) + 0.08*math.Cos(2*math.Pi*float64(i-n/2)/float64(n/2)) // Blackman
			raw[t] = s * w
			sum += raw[t]
		}
		for t := range raw {
			u.coef[p][t] = int32(math.Round(raw[t] / sum * 32768)) // each phase: gain 1
		}
	}
	return u
}

func (u *upsampler) push(l, r int16) {
	copy(u.hl[1:], u.hl[:upTaps-1])
	copy(u.hr[1:], u.hr[:upTaps-1])
	u.hl[0], u.hr[0] = int32(l), int32(r)
}

func (u *upsampler) phase(p int) (int16, int16) {
	var l, r int32
	c := &u.coef[p]
	for t := 0; t < upTaps; t++ {
		l += u.hl[t] * c[t] >> 4
		r += u.hr[t] * c[t] >> 4
	}
	return clip16(l >> 11), clip16(r >> 11)
}

func clip16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}
