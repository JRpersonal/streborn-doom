package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/ssh"
)

// agentInfo is what STR's agent says about the speaker.
type agentInfo struct {
	Version string `json:"version"`
	Model   string `json:"model"`
	Name    string `json:"friendlyName"`
	port    int
}

// findAgent asks STR's agent on the speaker who it is. Speakers answer on
// :8888 or, on some chassis, only on :17008.
func findAgent(host string) (agentInfo, error) {
	c := &http.Client{Timeout: 4 * time.Second}
	for _, port := range []int{17008, 8888} {
		resp, err := c.Get(fmt.Sprintf("http://%s:%d/api/agent/version", host, port))
		if err != nil {
			continue
		}
		var info agentInfo
		err = json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if err == nil && info.Version != "" {
			info.port = port
			return info, nil
		}
	}
	return agentInfo{}, fmt.Errorf("no STR speaker answers at %s (is STR installed, and is this the right address?)", host)
}

// shell is a root SSH connection to the speaker.
type shell struct{ c *ssh.Client }

// connect opens SSH through STR's own switch for it, then logs in. The
// speaker's root account has no password; that is the firmware's, not ours.
func connect(host string, info agentInfo) (*shell, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Post(fmt.Sprintf("http://%s:%d/api/agent/enable-ssh", host, info.port), "text/plain", nil)
	if err != nil {
		return nil, fmt.Errorf("could not ask the speaker to open SSH: %w", err)
	}
	resp.Body.Close()
	cfg := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.Password(""),
			ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return []string{""}, nil }),
		},
		// The speaker is on your own network and its host key changes with
		// every factory reset; there is nothing to pin it to.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
		// The speaker's dropbear speaks only older algorithms.
		Config: ssh.Config{
			KeyExchanges: []string{"curve25519-sha256@libssh.org", "ecdh-sha2-nistp256", "diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1"},
			Ciphers:      []string{"aes128-ctr", "aes256-ctr", "aes128-cbc", "3des-cbc"},
		},
		HostKeyAlgorithms: []string{"ssh-rsa", "rsa-sha2-256", "ssh-dss", "ecdsa-sha2-nistp256", "ssh-ed25519"},
	}
	var last error
	for i := 0; i < 10; i++ { // sshd may take a moment to come up
		cl, err := ssh.Dial("tcp", net.JoinHostPort(host, "22"), cfg)
		if err == nil {
			return &shell{c: cl}, nil
		}
		last = err
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("SSH to the speaker failed: %w", last)
}

func (s *shell) Close() { s.c.Close() }

// run executes cmd on the speaker with stdin, returning its output.
func (s *shell) run(cmd string, stdin io.Reader) (string, error) {
	sess, err := s.c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &out
	sess.Stdin = stdin
	err = sess.Run(cmd)
	return out.String(), err
}
