package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestAskHost(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"192.0.2.50\n", "192.0.2.50", false},
		{"  192.0.2.50 \r\n", "192.0.2.50", false},
		{"\n\n192.0.2.50\n", "192.0.2.50", false},
		{"192.0.2.50", "192.0.2.50", false},
		{"", "", true},
		{"\n  \n", "", true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		got, err := askHost(bufio.NewReader(strings.NewReader(c.in)), &out)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("askHost(%q) = %q, %v; want %q, err=%v", c.in, got, err, c.want, c.wantErr)
		}
		if !strings.HasPrefix(out.String(), "Speaker IP address: ") {
			t.Errorf("askHost(%q) prompt = %q", c.in, out.String())
		}
	}
}

func TestWaitForEnter(t *testing.T) {
	var out bytes.Buffer
	r := bufio.NewReader(strings.NewReader("\nrest"))
	waitForEnter(r, &out)
	if !strings.Contains(out.String(), "Press Enter to close.") {
		t.Errorf("prompt = %q", out.String())
	}
	if rest, _ := r.ReadString('\n'); rest != "rest" {
		t.Errorf("read past Enter: left %q", rest)
	}
}
