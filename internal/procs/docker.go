package procs

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The docker unix socket is the single permitted socket in the entire
// tool: local only, read only, and skipped when absent or when
// AGENTDASH_SKIP_DOCKER is set. Container memory comes from cgroup files
// (~1ms) instead of the ~2s `docker stats` sample.

const dockerSock = "/var/run/docker.sock"

// Sandbox is one running container for the sandboxes section.
type Sandbox struct {
	Name    string
	Profile string
	UpSecs  int64
	MemMiB  float64
}

func dockerClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", dockerSock)
			},
		},
	}
}

// DockerAvailable reports whether the sandboxes section applies at all.
func DockerAvailable() bool {
	if os.Getenv("AGENTDASH_SKIP_DOCKER") != "" {
		return false
	}
	st, err := os.Stat(dockerSock)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

var profileRe = regexp.MustCompile(`/\.hermes/profiles/([^/]+)/`)

// Sandboxes lists running containers via the local socket.
func Sandboxes(now int64) []Sandbox {
	cl := dockerClient()
	resp, err := cl.Get("http://docker/containers/json")
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var conts []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		Created int64    `json:"Created"`
		Mounts  []struct {
			Source string `json:"Source"`
		} `json:"Mounts"`
	}
	if json.NewDecoder(resp.Body).Decode(&conts) != nil {
		return nil
	}
	var out []Sandbox
	for _, c := range conts {
		name := c.ID[:12]
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		profile := "(default)"
		for _, m := range c.Mounts {
			if m := profileRe.FindStringSubmatch(m.Source); m != nil {
				profile = m[1]
				break
			}
		}
		out = append(out, Sandbox{
			Name:    name,
			Profile: profile,
			UpSecs:  now - c.Created,
			MemMiB:  containerMemMiB(c.ID),
		})
	}
	return out
}

// containerMemMiB reads current memory from the container's cgroup,
// trying the v2 and v1 layouts docker uses.
func containerMemMiB(id string) float64 {
	for _, p := range []string{
		"/sys/fs/cgroup/system.slice/docker-" + id + ".scope/memory.current",
		"/sys/fs/cgroup/memory/system.slice/docker-" + id + ".scope/memory.usage_in_bytes",
		"/sys/fs/cgroup/memory/docker/" + id + "/memory.usage_in_bytes",
		filepath.Join("/sys/fs/cgroup/docker", id, "memory.current"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			continue
		}
		return float64(n) / (1 << 20)
	}
	return 0
}
