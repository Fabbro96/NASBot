package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingDockerRunner answers `docker ps` with a canned payload and counts the
// probes it received, so a test can tell "the cache answered" from "a probe ran".
type countingDockerRunner struct {
	mu      sync.Mutex
	out     []byte
	err     error
	probes  int
	lastCmd string
}

func (r *countingDockerRunner) Exists(name string) bool { return true }

func (r *countingDockerRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastCmd = name
	for _, a := range args {
		if a == "--format" {
			r.probes++
		}
	}
	return r.out, r.err
}

func (r *countingDockerRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, nil
}

func (r *countingDockerRunner) Run(_ context.Context, _ string, _ ...string) error { return nil }

func (r *countingDockerRunner) Probes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probes
}

const oneRunningContainer = `{"Names":"fresh","Status":"Up 2 hours","State":"running","Image":"img","ID":"abc"}`

// dockerProbeRunner installs a runner that answers every `docker ps` with out/err
// and releases it when the test ends. Installing it is mandatory, not optional:
// getCachedContainerList shells out to Docker, and on a machine that happens to
// have Docker running the assertions below would otherwise read that machine's
// containers.
func dockerProbeRunner(t *testing.T, out string, err error) *countingDockerRunner {
	t.Helper()

	r := &countingDockerRunner{out: []byte(out), err: err}
	t.Cleanup(setCommandRunner(r))
	return r
}

// expiredDockerContext returns a context whose container cache is one TTL old and
// holds a container that must not survive the refresh.
func expiredDockerContext(t *testing.T) *AppContext {
	t.Helper()

	ctx := newTestAppContext() // Cache.DockerTTLSeconds = 60
	ctx.Docker.Cache.Containers = []ContainerInfo{{Name: "stale", Running: true}}
	ctx.Docker.Cache.LastUpdate = time.Now().Add(-2 * time.Minute)
	return ctx
}

// TestGetCachedContainerListExpiredCacheIsRefetched replaces the old
// TestDockerCache_Expiration, whose only assertion was `if len(list) != 0 {
// t.Logf(...) }`: an inverted condition behind a log, so it could neither pass
// nor fail. The invariant is that an expired cache is never served: the caller
// gets a fresh probe, and the stale entry is gone from both the answer and the
// cache.
func TestGetCachedContainerListExpiredCacheIsRefetched(t *testing.T) {
	ctx := expiredDockerContext(t)
	runner := dockerProbeRunner(t, oneRunningContainer, nil)

	list := getCachedContainerList(ctx)

	if runner.Probes() != 1 {
		t.Fatalf("expected exactly one docker probe, got %d", runner.Probes())
	}
	if len(list) != 1 || list[0].Name != "fresh" {
		t.Fatalf("expected the freshly probed list, got %+v", list)
	}
	for _, c := range list {
		if c.Name == "stale" {
			t.Fatalf("the expired cache entry was served: %+v", list)
		}
	}
	if cached := ctx.Docker.Cache.Containers; len(cached) != 1 || cached[0].Name != "fresh" {
		t.Errorf("cache was not refreshed with the probe result: %+v", cached)
	}
	if age := time.Since(ctx.Docker.Cache.LastUpdate); age > time.Minute {
		t.Errorf("cache timestamp was not re-stamped (age %v)", age)
	}
}

// TestGetCachedContainerListFreshCacheSkipsProbe is the other side of the TTL: a
// cache inside its window must answer without touching Docker. A regression here
// turns every /status into a `docker ps` fork.
func TestGetCachedContainerListFreshCacheSkipsProbe(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Docker.Cache.Containers = []ContainerInfo{{Name: "cached", Running: true}}
	ctx.Docker.Cache.LastUpdate = time.Now()
	runner := dockerProbeRunner(t, oneRunningContainer, nil)

	list := getCachedContainerList(ctx)

	if runner.Probes() != 0 {
		t.Errorf("a fresh cache must be served without probing, but %d probes ran", runner.Probes())
	}
	if len(list) != 1 || list[0].Name != "cached" {
		t.Errorf("expected the cached list, got %+v", list)
	}
}

// TestGetCachedContainerListFailedProbeDoesNotCacheEmptyList pins the invariant
// documented on getContainerList: when `docker ps` fails the cache must be left
// exactly as it was and the caller must be told "unknown", never "no containers".
// Caching the empty result under a fresh timestamp would report every container
// as down for the whole TTL, and the Docker watchdog would restart the world on
// a Docker daemon that was only restarting.
func TestGetCachedContainerListFailedProbeDoesNotCacheEmptyList(t *testing.T) {
	ctx := expiredDockerContext(t)
	runner := dockerProbeRunner(t, "", errors.New("docker: daemon not reachable"))

	before := ctx.Docker.Cache.LastUpdate
	list := getCachedContainerList(ctx)

	if runner.Probes() != 1 {
		t.Fatalf("expected one probe attempt, got %d", runner.Probes())
	}
	if list != nil {
		t.Errorf("a failed probe must return nil, not a list that reads as authoritative: %+v", list)
	}
	if ctx.Docker.Cache.LastUpdate != before {
		t.Error("a failed probe re-stamped the cache timestamp: a later reader would " +
			"treat the untouched stale list as fresh")
	}
	if cached := ctx.Docker.Cache.Containers; len(cached) != 1 || cached[0].Name != "stale" {
		t.Errorf("a failed probe must leave the cache untouched, got %+v", cached)
	}
}

// TestGetCachedContainerListRateLimitsProbesAfterFailure covers the cooldown: a
// Docker outage must not become a `docker ps` per caller. getStatusText, the
// Docker menu, the watchdog and the report all read the list, so an unrated
// retry is a fork storm on the one host that cannot afford it.
func TestGetCachedContainerListRateLimitsProbesAfterFailure(t *testing.T) {
	ctx := expiredDockerContext(t)
	runner := dockerProbeRunner(t, "", errors.New("docker: daemon not reachable"))

	getCachedContainerList(ctx)
	first := runner.Probes()
	if first != 1 {
		t.Fatalf("expected the first call to probe, got %d probes", first)
	}

	for i := 0; i < 4; i++ {
		getCachedContainerList(ctx)
	}
	if got := runner.Probes(); got != first {
		t.Errorf("probes inside the cooldown window: %d calls to the cache produced %d probes, want %d",
			5, got, first)
	}
}

// TestGetCachedContainerListRecoversAfterCooldown: the cooldown must expire, or
// one failed probe at boot would silence Docker for the rest of the process. The
// marker is advanced directly instead of by sleeping 30 seconds.
func TestGetCachedContainerListRecoversAfterCooldown(t *testing.T) {
	ctx := expiredDockerContext(t)

	down := dockerProbeRunner(t, "", errors.New("docker: daemon not reachable"))
	getCachedContainerList(ctx)
	if down.Probes() != 1 {
		t.Fatalf("expected the first call to probe, got %d probes", down.Probes())
	}

	// Age the failure marker past the cooldown instead of sleeping 30 seconds.
	dockerCacheErrMu.Lock()
	dockerCacheErrSince[ctx.Docker] = time.Now().Add(-2 * dockerCacheErrCooldown)
	dockerCacheErrMu.Unlock()
	t.Cleanup(func() {
		dockerCacheErrMu.Lock()
		delete(dockerCacheErrSince, ctx.Docker)
		dockerCacheErrMu.Unlock()
	})

	up := dockerProbeRunner(t, oneRunningContainer, nil)
	list := getCachedContainerList(ctx)

	if up.Probes() != 1 {
		t.Fatalf("after the cooldown the cache must probe again, got %d probes", up.Probes())
	}
	if len(list) != 1 || list[0].Name != "fresh" {
		t.Fatalf("expected the recovered list, got %+v", list)
	}
	if dockerProbeRecentlyFailed(ctx.Docker) {
		t.Error("a successful probe must clear the failure marker")
	}
}

// TestGetCachedContainerListIsolatesManagerState: the failure cooldown is keyed
// by *DockerManager, so one context's outage must not silence another's Docker.
// A shared key would make the second InitApp (the watchdog, a test) report "no
// containers" for as long as the first one was down.
func TestGetCachedContainerListIsolatesManagerState(t *testing.T) {
	down := expiredDockerContext(t)
	up := expiredDockerContext(t)
	if down.Docker == up.Docker {
		t.Fatal("the two contexts must not share a DockerManager, the test proves nothing")
	}

	dockerProbeRunner(t, "", errors.New("docker: daemon not reachable"))
	getCachedContainerList(down)

	if !dockerProbeRecentlyFailed(down.Docker) {
		t.Fatal("the failing probe did not mark the manager")
	}
	if dockerProbeRecentlyFailed(up.Docker) {
		t.Error("the failure marker leaked to a different DockerManager")
	}

	dockerCacheErrMu.Lock()
	delete(dockerCacheErrSince, down.Docker)
	dockerCacheErrMu.Unlock()
}

// TestGetContainerListPropagatesProbeError: getContainerList is the
// error-swallowing wrapper. It must answer nil on failure (its callers treat nil
// as "unknown") and must not fabricate an empty list, which would read as "the
// host has no containers" and fire the "container not found" alerts.
func TestGetContainerListPropagatesProbeError(t *testing.T) {
	dockerProbeRunner(t, "", errors.New("docker: command not found"))

	if got := getContainerList(); got != nil {
		t.Errorf("getContainerList on failure = %+v, want nil", got)
	}
}

// TestGetContainerListReturnsProbedContainers is the success half of the same
// wrapper.
func TestGetContainerListReturnsProbedContainers(t *testing.T) {
	dockerProbeRunner(t, oneRunningContainer, nil)

	got := getContainerList()
	if len(got) != 1 || got[0].Name != "fresh" || !got[0].Running {
		t.Errorf("getContainerList = %+v, want the one running container", got)
	}
}

// TestCopyContainerListNeverAliasesTheCache: the cache hands its slice to
// several readers while the collector replaces it. Returning the same backing
// array would let a caller mutate the cache and would be a data race the moment
// two callers hold it.
func TestCopyContainerListNeverAliasesTheCache(t *testing.T) {
	original := []ContainerInfo{{Name: "a", Running: true}, {Name: "b"}}

	copied := copyContainerList(original)
	if len(copied) != 2 || copied[0].Name != "a" {
		t.Fatalf("copyContainerList = %+v", copied)
	}
	copied[0].Name = "mutated"
	copied[1].Name = "mutated"

	if original[0].Name != "a" || original[1].Name != "b" {
		t.Errorf("the copy aliases the source: %+v", original)
	}
	if copyContainerList(nil) != nil {
		t.Error("copyContainerList(nil) must stay nil, not become an empty slice")
	}
}
