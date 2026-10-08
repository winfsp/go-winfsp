//go:build windows

package winfsp

import (
	"math"
	"testing"
	"time"
)

func TestTimeoutMillis(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want uint32
	}{
		{0, 0},
		{time.Nanosecond, 1},
		{time.Millisecond, 1},
		{time.Millisecond + 1, 2},
		{time.Second, 1000},
		{InfiniteTimeout, math.MaxUint32},
		{-time.Hour, math.MaxUint32},
		{math.MaxUint32 * time.Millisecond, math.MaxUint32},
		{math.MaxInt64, math.MaxUint32},
	}
	for _, tt := range tests {
		if got := timeoutMillis(tt.d); got != tt.want {
			t.Errorf("timeoutMillis(%v) = %d; want %d", tt.d, got, tt.want)
		}
	}
}
