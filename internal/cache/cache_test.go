package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return NewRedisCache(mr.Addr()), mr
}

func TestRedisCacheRoundtrip(t *testing.T) {
	ctx := context.Background()
	c, mr := newMiniCache(t)

	// miss
	val, ok, err := c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, val)

	// set → hit
	require.NoError(t, c.Set(ctx, "k1", "v1", time.Minute))
	val, ok, err = c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v1", val)

	// TTL 边界：过期后 miss
	require.NoError(t, c.Set(ctx, "k2", "v2", 10*time.Millisecond))
	mr.FastForward(20 * time.Millisecond)
	_, ok, err = c.Get(ctx, "k2")
	require.NoError(t, err)
	assert.False(t, ok, "过期后应 miss")

	// del → miss
	require.NoError(t, c.Set(ctx, "k3", "v3", time.Minute))
	require.NoError(t, c.Del(ctx, "k3"))
	_, ok, err = c.Get(ctx, "k3")
	require.NoError(t, err)
	assert.False(t, ok)

	// del 不存在的 key 也应成功
	require.NoError(t, c.Del(ctx, "k-not-exist"))
}

func TestRedisCacheServerDown(t *testing.T) {
	mr := miniredis.RunT(t)
	c := NewRedisCache(mr.Addr())
	ctx := context.Background()

	mr.Close() // 服务不可用

	_, _, err := c.Get(ctx, "k")
	require.Error(t, err)
	require.Error(t, c.Set(ctx, "k", "v", time.Minute))
	require.Error(t, c.Del(ctx, "k"))
}

func TestRedisCacheClose(t *testing.T) {
	c, _ := newMiniCache(t)
	assert.NoError(t, c.Close())
}

func TestNoopCache(t *testing.T) {
	ctx := context.Background()
	c := NoopCache{}

	val, ok, err := c.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, val)

	assert.NoError(t, c.Set(ctx, "k", "v", time.Minute))
	assert.NoError(t, c.Del(ctx, "k"))

	// 接口一致性哨兵
	var _ KVCache = c
	var _ KVCache = &RedisCache{}
}
