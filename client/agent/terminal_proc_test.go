//go:build !windows

package agent

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKillProcessTreeKillsChildren(t *testing.T) {
	c := exec.Command("sh", "-c", "sleep 60 & echo $!; wait")
	setNewProcessGroup(c)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	killProcessTree(c.Process, 200*time.Millisecond)
	_ = c.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(child, 0); err != nil {
			return // alt surec oldu (zombi ise init toplar)
		}
		// zombi (sh reaped degil) durumunu kontrol et
		if b, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(child)).Output(); strings.HasPrefix(strings.TrimSpace(string(b)), "Z") || len(strings.TrimSpace(string(b))) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("alt surec %d hala yasiyor", child)
}

func TestTerminalSessionCap(t *testing.T) {
	tm := &terminalManager{sessions: map[string]*terminalSession{}, maxSessions: 2}
	if tm.full() {
		t.Fatal("bos yonetici dolu gorunuyor")
	}
	tm.sessions["a"] = &terminalSession{id: "a"}
	tm.sessions["b"] = &terminalSession{id: "b"}
	if !tm.full() {
		t.Fatal("sinir dolunca full() true olmali")
	}
	if (&terminalManager{}).limit() != maxTerminalSessions {
		t.Fatal("varsayilan sinir yanlis")
	}
}
