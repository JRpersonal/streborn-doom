package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// The free shareware DOOM1.WAD (v1.9), unmodified, as Debian ships it in its
// doom-wad-shareware package. It is fetched from Debian and never stored in
// this repository; id Software's shareware licence allows passing on the
// unmodified file but not changing it.
const (
	wadSHA256 = "1d7d43be501e67d927e415e0b8f3e29c3bf33075e859721816f652a526cac771"
	debName   = "doom-wad-shareware_1.9.fixed-5_all.deb"
	debSHA256 = "5802f176c0303e228095b5312def53de602781cf4c53e79842257484a0d9e938"
)

var debMirrors = []string{
	"https://deb.debian.org/debian/pool/non-free/d/doom-wad-shareware/",
	"https://ftp.debian.org/debian/pool/non-free/d/doom-wad-shareware/",
}

// sharewareWAD returns DOOM1.WAD from the local cache, downloading it once.
func sharewareWAD() ([]byte, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	cache := filepath.Join(dir, "str-doom", "doom1.wad")
	if b, err := os.ReadFile(cache); err == nil && sum(b) == wadSHA256 {
		return b, nil
	}
	fmt.Println("Downloading the shareware DOOM1.WAD from Debian (once)...")
	var deb []byte
	var last error
	c := &http.Client{Timeout: 2 * time.Minute}
	for _, m := range debMirrors {
		resp, err := c.Get(m + debName)
		if err != nil {
			last = err
			continue
		}
		deb, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil && resp.StatusCode == 200 && sum(deb) == debSHA256 {
			break
		}
		last, deb = fmt.Errorf("%s: status %d or wrong checksum", m, resp.StatusCode), nil
	}
	if deb == nil {
		return nil, fmt.Errorf("could not download the shareware WAD (%v); pass your own with -wad", last)
	}
	wad, err := wadFromDeb(deb)
	if err != nil {
		return nil, err
	}
	if sum(wad) != wadSHA256 {
		return nil, fmt.Errorf("the shareware WAD does not match the expected checksum")
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
		_ = os.WriteFile(cache, wad, 0o644)
	}
	return wad, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// wadFromDeb pulls doom1.wad out of the .deb: an ar archive holding
// data.tar.xz.
func wadFromDeb(deb []byte) ([]byte, error) {
	r := bufio.NewReader(bytes.NewReader(deb))
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "!<arch>\n" {
		return nil, fmt.Errorf("not a .deb")
	}
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return nil, fmt.Errorf("data.tar.xz not found in the .deb")
		}
		name := strings.TrimRight(strings.TrimSpace(string(hdr[0:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("broken .deb")
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		if size%2 == 1 {
			r.ReadByte()
		}
		if name != "data.tar.xz" {
			continue
		}
		xr, err := xz.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(xr)
		for {
			h, err := tr.Next()
			if err != nil {
				return nil, fmt.Errorf("doom1.wad not found in the package")
			}
			if strings.HasSuffix(strings.ToLower(h.Name), "/doom1.wad") {
				return io.ReadAll(tr)
			}
		}
	}
}
