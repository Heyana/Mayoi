package main

import (
	"os"
	"testing"
	"time"
)

func TestPollingPausesWhenBlurred(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	hs := newHarness(t, true)
	hs.gitRepo()
	hs.settleGit()

	now := time.Now()
	hs.a.gitClock = func() time.Time { return now }
	hs.a.git.read = now
	hs.a.git.checked = now

	// Blur the window and advance the clock past both statusEvery and metaEvery.
	hs.a.setBlurred(true)
	now = now.Add(statusEvery + metaEvery + time.Second)
	hs.a.repository().refreshGit()
	if hs.a.git.statusRead.Running() || hs.a.git.metaRead.Running() {
		t.Fatal("periodic git reads started while blurred")
	}

	// Returning to focus resumes periodic reads.
	hs.a.setBlurred(false)
	hs.a.repository().refreshGit()
	if !hs.a.git.statusRead.Running() && !hs.a.git.metaRead.Running() {
		t.Fatal("periodic git reads did not resume after focus")
	}
	hs.settleGit()
}

func TestSessionsSkipsLivePollWithoutHC(t *testing.T) {
	hs := newHarness(t, true)
	hs.a.settings.TerminalBackend = ""

	// Blurred window does not start session scanning.
	hs.a.setBlurred(true)
	hs.a.readSessions()
	if hs.a.sessions.read.Running() || hs.a.sessions.live.Running() {
		t.Fatal("readSessions started polling while blurred")
	}

	// Focused without hc: scans sessions, but never schedules the 3s hc live poll.
	hs.a.setBlurred(false)
	hs.a.readSessions()
	if hs.a.sessions.live.Running() || hs.a.sessions.liveSeen {
		t.Fatal("hc live poller ran without herdr-core backend")
	}
	hs.settle()
	if hs.a.sessions.liveSeen {
		t.Fatal("hc live poller completed without herdr-core backend")
	}
}
