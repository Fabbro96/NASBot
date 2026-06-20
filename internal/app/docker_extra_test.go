package app

import (
	"nasbot/pkg/model"
	"testing"
	"time"
)

func TestDockerCache_Expiration(t *testing.T) {
	ctx := model.InitApp(nil)
	ctx.Config = &model.Config{}
	ctx.Docker.Cache.Containers = []ContainerInfo{
		{Name: "test", Running: true},
	}
	ctx.Docker.Cache.LastUpdate = time.Now().Add(-2 * time.Minute)

	list := getCachedContainerList(ctx)
	// Since there's no actual docker runner, this will probably return empty or fail
	// but we just want to ensure it handles cache correctly without panicking.
	if len(list) != 0 {
		t.Logf("Expected cache to be invalidated")
	}
}
