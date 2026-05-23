package audio

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

const debounceDuration = 2 * time.Second

// Player manages playback of a pre-generated chime sound.
// All errors are silent — audio is best-effort.
type Player struct {
	chimePath  string
	playCmd    string
	lastPlayed time.Time
	mu         sync.Mutex
}

// resolvePlayCmd returns the platform audio command, or "" if none is available.
var resolvePlayCmd = defaultResolvePlayCmd

func defaultResolvePlayCmd() string {
	switch runtime.GOOS {
	case "darwin":
		return "afplay"
	case "linux":
		for _, cmd := range []string{"paplay", "aplay"} {
			if _, err := exec.LookPath(cmd); err == nil {
				return cmd
			}
		}
		return ""
	default:
		return ""
	}
}

// NewPlayer generates a chime WAV file and returns a Player that can play it.
func NewPlayer() (*Player, error) {
	path, err := GenerateChime()
	if err != nil {
		return nil, err
	}
	return &Player{chimePath: path, playCmd: resolvePlayCmd()}, nil
}

// Play plays the chime sound if at least 2 seconds have elapsed since the last play.
// Never blocks — playback runs in a goroutine. Errors are silently ignored.
func (p *Player) Play() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.playCmd == "" {
		return
	}

	if time.Since(p.lastPlayed) < debounceDuration {
		return
	}
	p.lastPlayed = time.Now()

	go func() {
		_ = exec.Command(p.playCmd, p.chimePath).Run()
	}()
}

// LastPlayedTime returns the timestamp of the last successful Play call.
func (p *Player) LastPlayedTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastPlayed
}

// Close removes the temporary chime file.
func (p *Player) Close() {
	_ = os.Remove(p.chimePath)
}
